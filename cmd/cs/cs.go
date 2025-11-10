package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
)

const (
	centralSystemPort    = 8887
	centralSystemPath    = "/{ws}"
	httpListenAddress    = ":8080"
	ocppRequestTimeout   = 10 * time.Second
	connectionEventName  = "Central system"
	maxMeterValueHistory = 10
)

// CSApp wires the OCPP central system and the HTTP frontend/API.
type CSApp struct {
	central      ocpp16.CentralSystem
	mu           sync.RWMutex
	chargePoints map[string]*chargePointState
}

type chargePointState struct {
	ID                       string
	Connected                bool
	Vendor                   string
	Model                    string
	FirmwareVersion          string
	LastBoot                 time.Time
	LastHeartbeat            time.Time
	LastMeterValues          time.Time
	LastMeterValueEntries    int
	LastConnectedAt          time.Time
	LastDisconnectedAt       time.Time
	Connectors               map[int]*connectorState
	ActiveTransactions       map[int]*transactionState
	LastCompletedTransaction *finishedTransaction
	MeterValues              map[int][]meterValueRecord
}

type connectorState struct {
	Status    string
	Error     string
	UpdatedAt time.Time
}

type transactionState struct {
	ID         int
	IdTag      string
	StartedAt  time.Time
	MeterStart int
}

type finishedTransaction struct {
	ID          int
	ConnectorID int
	IdTag       string
	Reason      string
	StoppedAt   time.Time
	MeterStart  int
	MeterStop   int
	EnergyWh    int
	EnergyKWh   float64
}

type meterValueRecord struct {
	Timestamp time.Time
	Samples   []sampledValueRecord
}

type sampledValueRecord struct {
	Value     string
	Unit      string
	Measurand string
	Phase     string
	Context   string
	Format    string
	Location  string
}

// CentralSystemHandler implements OCPP 1.6 core message handlers.
type CentralSystemHandler struct {
	app *CSApp
}

func main() {
	app := NewCSApp()
	app.Start()
	select {} // keep running
}

// NewCSApp creates a central system instance with state tracking.
func NewCSApp() *CSApp {
	cs := ocpp16.NewCentralSystem(nil, nil)
	app := &CSApp{
		central:      cs,
		chargePoints: make(map[string]*chargePointState),
	}

	handler := &CentralSystemHandler{app: app}
	cs.SetCoreHandler(handler)
	cs.SetNewChargePointHandler(app.handleChargePointConnected)
	cs.SetChargePointDisconnectedHandler(app.handleChargePointDisconnected)

	return app
}

// Start runs both the OCPP central system listener and the HTTP server.
func (a *CSApp) Start() {
	go a.startCentralSystem()
	go a.startHTTP()
}

func (a *CSApp) startCentralSystem() {
	log.Printf("%s: listening for charge points on ws://localhost:%d%s", connectionEventName, centralSystemPort, centralSystemPath)
	a.central.Start(centralSystemPort, centralSystemPath)
}

func (a *CSApp) startHTTP() {
	server := &http.Server{
		Addr:    httpListenAddress,
		Handler: a.routes(),
	}
	log.Printf("%s UI: available at http://localhost%s", connectionEventName, httpListenAddress)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("http server stopped: %v", err)
	}
}

func (a *CSApp) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chargepoints", a.handleChargePoints)
	mux.HandleFunc("/api/remote-start", a.handleRemoteStart)
	mux.HandleFunc("/api/remote-stop", a.handleRemoteStop)
	mux.HandleFunc("/api/reset", a.handleReset)
	mux.HandleFunc("/api/unlock", a.handleUnlock)
	a.mountStatic(mux)
	return mux
}

func (a *CSApp) mountStatic(mux *http.ServeMux) {
	root, err := fs.Sub(embeddedWebFS, "web")
	if err != nil {
		log.Printf("static assets unavailable: %v", err)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("Central system UI bundle not found. API is available under /api/."))
		})
		return
	}

	staticFS, err := fs.Sub(root, "static")
	if err != nil {
		log.Printf("static assets missing: %v", err)
	} else {
		fileServer := http.FileServer(http.FS(staticFS))
		mux.Handle("/static/", http.StripPrefix("/static/", fileServer))
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(w, "UI bundle missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
}

// --- HTTP handlers --------------------------------------------------------------------------

func (a *CSApp) handleChargePoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, a.listChargePoints())
}

func (a *CSApp) handleRemoteStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req remoteStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	req.trim()
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.isChargePointConnected(req.ChargePointID) {
		writeError(w, http.StatusBadRequest, "charge point is not connected")
		return
	}

	resultCh := make(chan remoteStartResult, 1)
	var props []func(*core.RemoteStartTransactionRequest)
	if req.ConnectorID != nil {
		connector := *req.ConnectorID
		props = append(props, func(request *core.RemoteStartTransactionRequest) {
			request.ConnectorId = &connector
		})
	}

	err := a.central.RemoteStartTransaction(req.ChargePointID, func(conf *core.RemoteStartTransactionConfirmation, err error) {
		resultCh <- remoteStartResult{confirmation: conf, err: err}
	}, req.IdTag, props...)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			writeError(w, http.StatusBadGateway, result.err.Error())
			return
		}
		resp := map[string]string{
			"status": string(result.confirmation.Status),
		}
		writeJSON(w, http.StatusOK, resp)
	case <-time.After(ocppRequestTimeout):
		writeError(w, http.StatusGatewayTimeout, "no response from charge point")
	}
}

func (a *CSApp) handleRemoteStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req remoteStopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	req.trim()
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.isChargePointConnected(req.ChargePointID) {
		writeError(w, http.StatusBadRequest, "charge point is not connected")
		return
	}

	resultCh := make(chan remoteStopResult, 1)
	err := a.central.RemoteStopTransaction(req.ChargePointID, func(conf *core.RemoteStopTransactionConfirmation, err error) {
		resultCh <- remoteStopResult{confirmation: conf, err: err}
	}, req.TransactionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			writeError(w, http.StatusBadGateway, result.err.Error())
			return
		}
		resp := map[string]string{
			"status": string(result.confirmation.Status),
		}
		writeJSON(w, http.StatusOK, resp)
	case <-time.After(ocppRequestTimeout):
		writeError(w, http.StatusGatewayTimeout, "no response from charge point")
	}
}

func (a *CSApp) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	req.trim()
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.isChargePointConnected(req.ChargePointID) {
		writeError(w, http.StatusBadRequest, "charge point is not connected")
		return
	}

	resetType := core.ResetTypeSoft
	if strings.EqualFold(req.Type, string(core.ResetTypeHard)) {
		resetType = core.ResetTypeHard
	}

	resultCh := make(chan resetResult, 1)
	err := a.central.Reset(req.ChargePointID, func(conf *core.ResetConfirmation, err error) {
		resultCh <- resetResult{confirmation: conf, err: err}
	}, resetType)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			writeError(w, http.StatusBadGateway, result.err.Error())
			return
		}
		resp := map[string]string{
			"status": string(result.confirmation.Status),
		}
		writeJSON(w, http.StatusOK, resp)
	case <-time.After(ocppRequestTimeout):
		writeError(w, http.StatusGatewayTimeout, "no response from charge point")
	}
}

func (a *CSApp) handleUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req unlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON payload")
		return
	}
	req.trim()
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.isChargePointConnected(req.ChargePointID) {
		writeError(w, http.StatusBadRequest, "charge point is not connected")
		return
	}

	resultCh := make(chan unlockResult, 1)
	err := a.central.UnlockConnector(req.ChargePointID, func(conf *core.UnlockConnectorConfirmation, err error) {
		resultCh <- unlockResult{confirmation: conf, err: err}
	}, req.ConnectorID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			writeError(w, http.StatusBadGateway, result.err.Error())
			return
		}
		resp := map[string]string{
			"status": string(result.confirmation.Status),
		}
		writeJSON(w, http.StatusOK, resp)
	case <-time.After(ocppRequestTimeout):
		writeError(w, http.StatusGatewayTimeout, "no response from charge point")
	}
}

// --- Central System event handlers ----------------------------------------------------------

func (a *CSApp) handleChargePointConnected(conn ocpp16.ChargePointConnection) {
	id := conn.ID()
	log.Printf("charge point connected: %s (%s)", id, conn.RemoteAddr())
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.ensureStateLocked(id)
	state.Connected = true
	state.LastConnectedAt = time.Now()
}

func (a *CSApp) handleChargePointDisconnected(conn ocpp16.ChargePointConnection) {
	id := conn.ID()
	log.Printf("charge point disconnected: %s", id)
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.ensureStateLocked(id)
	state.Connected = false
	state.LastDisconnectedAt = time.Now()
	state.ActiveTransactions = make(map[int]*transactionState)
}

// OnBootNotification handles the BootNotification request.
func (h *CentralSystemHandler) OnBootNotification(chargePointID string, request *core.BootNotificationRequest) (*core.BootNotificationConfirmation, error) {
	log.Printf("Charge Point %s booted. Model=%s Vendor=%s", chargePointID, request.ChargePointModel, request.ChargePointVendor)
	h.app.recordBootNotification(chargePointID, request)
	return core.NewBootNotificationConfirmation(
		types.NewDateTime(time.Now()),
		300, core.RegistrationStatusAccepted), nil
}

// OnAuthorize handles the Authorize request.
func (h *CentralSystemHandler) OnAuthorize(chargePointID string, request *core.AuthorizeRequest) (*core.AuthorizeConfirmation, error) {
	log.Printf("Authorize requested by %s for idTag=%s", chargePointID, request.IdTag)
	return core.NewAuthorizationConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted)), nil
}

// OnHeartbeat handles Heartbeat requests.
func (h *CentralSystemHandler) OnHeartbeat(chargePointID string, request *core.HeartbeatRequest) (*core.HeartbeatConfirmation, error) {
	h.app.recordHeartbeat(chargePointID)
	return core.NewHeartbeatConfirmation(types.NewDateTime(time.Now())), nil
}

// OnDataTransfer handles DataTransfer requests.
func (h *CentralSystemHandler) OnDataTransfer(chargePointID string, request *core.DataTransferRequest) (*core.DataTransferConfirmation, error) {
	log.Printf("Received DataTransfer from %s vendorId=%s messageId=%s", chargePointID, request.VendorId, request.MessageId)
	return core.NewDataTransferConfirmation(core.DataTransferStatusAccepted), nil
}

// OnMeterValues handles MeterValues requests.
func (h *CentralSystemHandler) OnMeterValues(chargePointID string, request *core.MeterValuesRequest) (*core.MeterValuesConfirmation, error) {
	log.Printf("Received MeterValues from %s for connector %d (%d entries)", chargePointID, request.ConnectorId, len(request.MeterValue))
	h.app.recordMeterValues(chargePointID, request)
	return core.NewMeterValuesConfirmation(), nil
}

// OnStatusNotification handles StatusNotification requests.
func (h *CentralSystemHandler) OnStatusNotification(chargePointID string, request *core.StatusNotificationRequest) (*core.StatusNotificationConfirmation, error) {
	log.Printf("Status update from %s: connector %d status=%s error=%s", chargePointID, request.ConnectorId, request.Status, request.ErrorCode)
	h.app.recordStatusNotification(chargePointID, request)
	return core.NewStatusNotificationConfirmation(), nil
}

// OnStartTransaction handles StartTransaction requests.
func (h *CentralSystemHandler) OnStartTransaction(chargePointID string, request *core.StartTransactionRequest) (*core.StartTransactionConfirmation, error) {
	log.Printf("StartTransaction from %s: connector %d idTag=%s", chargePointID, request.ConnectorId, request.IdTag)
	transactionID := h.app.recordStartTransaction(chargePointID, request)
	return core.NewStartTransactionConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted), transactionID), nil
}

// OnStopTransaction handles StopTransaction requests.
func (h *CentralSystemHandler) OnStopTransaction(chargePointID string, request *core.StopTransactionRequest) (*core.StopTransactionConfirmation, error) {
	log.Printf("StopTransaction from %s: transaction %d reason=%s", chargePointID, request.TransactionId, request.Reason)
	h.app.recordStopTransaction(chargePointID, request)
	return core.NewStopTransactionConfirmation(), nil
}

// --- Application state updates --------------------------------------------------------------

func (a *CSApp) recordBootNotification(id string, req *core.BootNotificationRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.ensureStateLocked(id)
	state.Connected = true
	state.LastBoot = time.Now()
	state.Vendor = req.ChargePointVendor
	state.Model = req.ChargePointModel
	state.FirmwareVersion = req.FirmwareVersion
}

func (a *CSApp) recordHeartbeat(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.ensureStateLocked(id)
	state.LastHeartbeat = time.Now()
}

func (a *CSApp) recordMeterValues(id string, req *core.MeterValuesRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.ensureStateLocked(id)
	entries := len(req.MeterValue)
	if state.MeterValues == nil {
		state.MeterValues = make(map[int][]meterValueRecord)
	}
	var latest time.Time
	for _, meterValue := range req.MeterValue {
		timestamp := time.Now()
		if meterValue.Timestamp != nil && !meterValue.Timestamp.IsZero() {
			timestamp = meterValue.Timestamp.Time
		}
		record := meterValueRecord{Timestamp: timestamp}
		for _, sample := range meterValue.SampledValue {
			record.Samples = append(record.Samples, sampledValueRecord{
				Value:     sample.Value,
				Unit:      string(sample.Unit),
				Measurand: string(sample.Measurand),
				Phase:     string(sample.Phase),
				Context:   string(sample.Context),
				Format:    string(sample.Format),
				Location:  string(sample.Location),
			})
		}
		values := append(state.MeterValues[req.ConnectorId], record)
		if len(values) > maxMeterValueHistory {
			values = values[len(values)-maxMeterValueHistory:]
		}
		state.MeterValues[req.ConnectorId] = values
		if latest.IsZero() || timestamp.After(latest) {
			latest = timestamp
		}
	}
	if latest.IsZero() {
		latest = time.Now()
	}
	state.LastMeterValues = latest
	state.LastMeterValueEntries = entries
}

func (a *CSApp) recordStatusNotification(id string, req *core.StatusNotificationRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.ensureStateLocked(id)
	if state.Connectors == nil {
		state.Connectors = make(map[int]*connectorState)
	}
	conn := state.Connectors[req.ConnectorId]
	if conn == nil {
		conn = &connectorState{}
		state.Connectors[req.ConnectorId] = conn
	}
	conn.Status = string(req.Status)
	conn.Error = string(req.ErrorCode)
	conn.UpdatedAt = time.Now()
}

func (a *CSApp) recordStartTransaction(id string, req *core.StartTransactionRequest) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.ensureStateLocked(id)
	if state.ActiveTransactions == nil {
		state.ActiveTransactions = make(map[int]*transactionState)
	}
	transactionID := int(time.Now().Unix())
	state.ActiveTransactions[req.ConnectorId] = &transactionState{
		ID:         transactionID,
		IdTag:      req.IdTag,
		StartedAt:  time.Now(),
		MeterStart: req.MeterStart,
	}
	return transactionID
}

func (a *CSApp) recordStopTransaction(id string, req *core.StopTransactionRequest) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.ensureStateLocked(id)
	var connectorID int
	var meterStart int
	if state.ActiveTransactions != nil {
		for cid, tx := range state.ActiveTransactions {
			if tx.ID == req.TransactionId {
				connectorID = cid
				meterStart = tx.MeterStart
				delete(state.ActiveTransactions, cid)
				break
			}
		}
	}
	stoppedAt := time.Now()
	if req.Timestamp != nil && !req.Timestamp.IsZero() {
		stoppedAt = req.Timestamp.Time
	}
	meterStop := req.MeterStop
	if meterStop < 0 {
		meterStop = 0
	}
	energyWh := 0
	if meterStop >= meterStart && meterStop > 0 {
		energyWh = meterStop - meterStart
	}
	energyKWh := float64(energyWh) / 1000
	state.LastCompletedTransaction = &finishedTransaction{
		ID:          req.TransactionId,
		ConnectorID: connectorID,
		IdTag:       req.IdTag,
		Reason:      string(req.Reason),
		StoppedAt:   stoppedAt,
		MeterStart:  meterStart,
		MeterStop:   meterStop,
		EnergyWh:    energyWh,
		EnergyKWh:   energyKWh,
	}
}

func (a *CSApp) ensureStateLocked(id string) *chargePointState {
	state, ok := a.chargePoints[id]
	if !ok {
		state = &chargePointState{
			ID:                 id,
			Connectors:         make(map[int]*connectorState),
			ActiveTransactions: make(map[int]*transactionState),
			MeterValues:        make(map[int][]meterValueRecord),
		}
		a.chargePoints[id] = state
	}
	if state.Connectors == nil {
		state.Connectors = make(map[int]*connectorState)
	}
	if state.ActiveTransactions == nil {
		state.ActiveTransactions = make(map[int]*transactionState)
	}
	if state.MeterValues == nil {
		state.MeterValues = make(map[int][]meterValueRecord)
	}
	return state
}

func (a *CSApp) isChargePointConnected(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	state, ok := a.chargePoints[id]
	return ok && state.Connected
}

// --- View helpers --------------------------------------------------------------------------

func (a *CSApp) listChargePoints() []chargePointView {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ids := make([]string, 0, len(a.chargePoints))
	for id := range a.chargePoints {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make([]chargePointView, 0, len(ids))
	for _, id := range ids {
		result = append(result, a.chargePoints[id].toView())
	}
	return result
}

type chargePointView struct {
	ID                       string                    `json:"id"`
	Connected                bool                      `json:"connected"`
	Vendor                   string                    `json:"vendor"`
	Model                    string                    `json:"model"`
	FirmwareVersion          string                    `json:"firmwareVersion,omitempty"`
	LastBoot                 string                    `json:"lastBoot"`
	LastHeartbeat            string                    `json:"lastHeartbeat"`
	LastMeterValues          string                    `json:"lastMeterValues"`
	LastMeterValueEntries    int                       `json:"lastMeterValueEntries"`
	LastConnectedAt          string                    `json:"lastConnectedAt"`
	LastDisconnectedAt       string                    `json:"lastDisconnectedAt"`
	Connectors               []connectorView           `json:"connectors"`
	ActiveTransactions       []transactionView         `json:"activeTransactions"`
	LastCompletedTransaction *transactionSummary       `json:"lastCompletedTransaction,omitempty"`
	MeterValues              []meterValueConnectorView `json:"meterValues"`
}

type connectorView struct {
	ConnectorID int    `json:"connectorId"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	UpdatedAt   string `json:"updatedAt"`
}

type transactionView struct {
	ConnectorID   int    `json:"connectorId"`
	TransactionID int    `json:"transactionId"`
	IdTag         string `json:"idTag"`
	StartedAt     string `json:"startedAt"`
}

type transactionSummary struct {
	ConnectorID   int    `json:"connectorId"`
	TransactionID int    `json:"transactionId"`
	IdTag         string `json:"idTag"`
	Reason        string `json:"reason"`
	StoppedAt     string `json:"stoppedAt"`
	MeterStart    int    `json:"meterStart,omitempty"`
	MeterStop     int    `json:"meterStop,omitempty"`
	EnergyWh      int    `json:"energyWh,omitempty"`
	EnergyKWh     string `json:"energyKWh,omitempty"`
}

type meterValueConnectorView struct {
	ConnectorID int                   `json:"connectorId"`
	Entries     []meterValueEntryView `json:"entries"`
}

type meterValueEntryView struct {
	Timestamp     string             `json:"timestamp"`
	SampledValues []sampledValueView `json:"sampledValues"`
}

type sampledValueView struct {
	Value     string `json:"value"`
	Unit      string `json:"unit,omitempty"`
	Measurand string `json:"measurand,omitempty"`
	Phase     string `json:"phase,omitempty"`
	Context   string `json:"context,omitempty"`
	Format    string `json:"format,omitempty"`
	Location  string `json:"location,omitempty"`
}

func (c *chargePointState) toView() chargePointView {
	view := chargePointView{
		ID:                    c.ID,
		Connected:             c.Connected,
		Vendor:                c.Vendor,
		Model:                 c.Model,
		FirmwareVersion:       c.FirmwareVersion,
		LastBoot:              formatTime(c.LastBoot),
		LastHeartbeat:         formatTime(c.LastHeartbeat),
		LastMeterValues:       formatTime(c.LastMeterValues),
		LastMeterValueEntries: c.LastMeterValueEntries,
		LastConnectedAt:       formatTime(c.LastConnectedAt),
		LastDisconnectedAt:    formatTime(c.LastDisconnectedAt),
	}

	if len(c.Connectors) > 0 {
		connectorIDs := make([]int, 0, len(c.Connectors))
		for id := range c.Connectors {
			connectorIDs = append(connectorIDs, id)
		}
		sort.Ints(connectorIDs)
		for _, id := range connectorIDs {
			conn := c.Connectors[id]
			view.Connectors = append(view.Connectors, connectorView{
				ConnectorID: id,
				Status:      conn.Status,
				Error:       conn.Error,
				UpdatedAt:   formatTime(conn.UpdatedAt),
			})
		}
	}

	if len(c.ActiveTransactions) > 0 {
		connectorIDs := make([]int, 0, len(c.ActiveTransactions))
		for connectorID := range c.ActiveTransactions {
			connectorIDs = append(connectorIDs, connectorID)
		}
		sort.Ints(connectorIDs)
		for _, connectorID := range connectorIDs {
			tx := c.ActiveTransactions[connectorID]
			view.ActiveTransactions = append(view.ActiveTransactions, transactionView{
				ConnectorID:   connectorID,
				TransactionID: tx.ID,
				IdTag:         tx.IdTag,
				StartedAt:     formatTime(tx.StartedAt),
			})
		}
	}

	if c.LastCompletedTransaction != nil {
		view.LastCompletedTransaction = &transactionSummary{
			ConnectorID:   c.LastCompletedTransaction.ConnectorID,
			TransactionID: c.LastCompletedTransaction.ID,
			IdTag:         c.LastCompletedTransaction.IdTag,
			Reason:        c.LastCompletedTransaction.Reason,
			StoppedAt:     formatTime(c.LastCompletedTransaction.StoppedAt),
			MeterStart:    c.LastCompletedTransaction.MeterStart,
			MeterStop:     c.LastCompletedTransaction.MeterStop,
			EnergyWh:      c.LastCompletedTransaction.EnergyWh,
			EnergyKWh:     formatEnergyKWh(c.LastCompletedTransaction.EnergyWh),
		}
	}

	if len(c.MeterValues) > 0 {
		connectorIDs := make([]int, 0, len(c.MeterValues))
		for connectorID := range c.MeterValues {
			connectorIDs = append(connectorIDs, connectorID)
		}
		sort.Ints(connectorIDs)
		for _, connectorID := range connectorIDs {
			records := c.MeterValues[connectorID]
			if len(records) == 0 {
				continue
			}
			history := meterValueConnectorView{ConnectorID: connectorID}
			for idx := len(records) - 1; idx >= 0; idx-- {
				record := records[idx]
				entry := meterValueEntryView{Timestamp: formatTime(record.Timestamp)}
				for _, sample := range record.Samples {
					entry.SampledValues = append(entry.SampledValues, sampledValueView{
						Value:     sample.Value,
						Unit:      sample.Unit,
						Measurand: sample.Measurand,
						Phase:     sample.Phase,
						Context:   sample.Context,
						Format:    sample.Format,
						Location:  sample.Location,
					})
				}
				history.Entries = append(history.Entries, entry)
			}
			view.MeterValues = append(view.MeterValues, history)
		}
	}

	return view
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatEnergyKWh(wh int) string {
	if wh <= 0 {
		return ""
	}
	value := float64(wh) / 1000
	formatted := fmt.Sprintf("%.3f", value)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	return formatted
}

// --- Input payloads -------------------------------------------------------------------------

type remoteStartRequest struct {
	ChargePointID string `json:"chargePointId"`
	IdTag         string `json:"idTag"`
	ConnectorID   *int   `json:"connectorId,omitempty"`
}

func (r *remoteStartRequest) trim() {
	r.ChargePointID = strings.TrimSpace(r.ChargePointID)
	r.IdTag = strings.TrimSpace(r.IdTag)
}

func (r *remoteStartRequest) validate() error {
	if r.ChargePointID == "" {
		return errFieldRequired("chargePointId")
	}
	if r.IdTag == "" {
		return errFieldRequired("idTag")
	}
	if r.ConnectorID != nil && *r.ConnectorID <= 0 {
		return errFieldPositive("connectorId")
	}
	return nil
}

type remoteStopRequest struct {
	ChargePointID string `json:"chargePointId"`
	TransactionID int    `json:"transactionId"`
}

func (r *remoteStopRequest) trim() {
	r.ChargePointID = strings.TrimSpace(r.ChargePointID)
}

func (r *remoteStopRequest) validate() error {
	if r.ChargePointID == "" {
		return errFieldRequired("chargePointId")
	}
	if r.TransactionID <= 0 {
		return errFieldPositive("transactionId")
	}
	return nil
}

type resetRequest struct {
	ChargePointID string `json:"chargePointId"`
	Type          string `json:"type"`
}

func (r *resetRequest) trim() {
	r.ChargePointID = strings.TrimSpace(r.ChargePointID)
	r.Type = strings.TrimSpace(r.Type)
}

func (r *resetRequest) validate() error {
	if r.ChargePointID == "" {
		return errFieldRequired("chargePointId")
	}
	if r.Type == "" {
		return errFieldRequired("type")
	}
	if !strings.EqualFold(r.Type, string(core.ResetTypeSoft)) && !strings.EqualFold(r.Type, string(core.ResetTypeHard)) {
		return errFieldInvalid("type")
	}
	return nil
}

type unlockRequest struct {
	ChargePointID string `json:"chargePointId"`
	ConnectorID   int    `json:"connectorId"`
}

func (r *unlockRequest) trim() {
	r.ChargePointID = strings.TrimSpace(r.ChargePointID)
}

func (r *unlockRequest) validate() error {
	if r.ChargePointID == "" {
		return errFieldRequired("chargePointId")
	}
	if r.ConnectorID <= 0 {
		return errFieldPositive("connectorId")
	}
	return nil
}

// --- HTTP helpers --------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to encode JSON response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func errFieldRequired(field string) error {
	return &validationError{message: field + " is required"}
}

func errFieldPositive(field string) error {
	return &validationError{message: field + " must be greater than zero"}
}

func errFieldInvalid(field string) error {
	return &validationError{message: field + " is invalid"}
}

type validationError struct {
	message string
}

func (e *validationError) Error() string {
	return e.message
}

// --- async helpers -------------------------------------------------------------------------

type remoteStartResult struct {
	confirmation *core.RemoteStartTransactionConfirmation
	err          error
}

type remoteStopResult struct {
	confirmation *core.RemoteStopTransactionConfirmation
	err          error
}

type resetResult struct {
	confirmation *core.ResetConfirmation
	err          error
}

type unlockResult struct {
	confirmation *core.UnlockConnectorConfirmation
	err          error
}
