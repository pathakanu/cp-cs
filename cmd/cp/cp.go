package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
)

type ChargePointHandler struct {
	cp              ocpp16.ChargePoint
	mu              sync.Mutex
	activeTxID      int
	activeConnector int
	activeIdTag     string
	meterStart      int
}

func NewChargePointHandler(cp ocpp16.ChargePoint) *ChargePointHandler {
	return &ChargePointHandler{cp: cp}
}

// Central System requests → always acknowledge with basic responses.
func (h *ChargePointHandler) OnChangeAvailability(request *core.ChangeAvailabilityRequest) (*core.ChangeAvailabilityConfirmation, error) {
	log.Printf("ChangeAvailability requested: connector=%d type=%s", request.ConnectorId, request.Type)
	return core.NewChangeAvailabilityConfirmation(core.AvailabilityStatusAccepted), nil
}

func (h *ChargePointHandler) OnChangeConfiguration(request *core.ChangeConfigurationRequest) (*core.ChangeConfigurationConfirmation, error) {
	log.Printf("ChangeConfiguration requested: key=%s value=%s", request.Key, request.Value)
	return core.NewChangeConfigurationConfirmation(core.ConfigurationStatusAccepted), nil
}

func (h *ChargePointHandler) OnClearCache(request *core.ClearCacheRequest) (*core.ClearCacheConfirmation, error) {
	log.Println("ClearCache requested")
	return core.NewClearCacheConfirmation(core.ClearCacheStatusAccepted), nil
}

func (h *ChargePointHandler) OnDataTransfer(request *core.DataTransferRequest) (*core.DataTransferConfirmation, error) {
	log.Printf("DataTransfer received: vendor=%s messageId=%s", request.VendorId, request.MessageId)
	return core.NewDataTransferConfirmation(core.DataTransferStatusAccepted), nil
}

func (h *ChargePointHandler) OnGetConfiguration(request *core.GetConfigurationRequest) (*core.GetConfigurationConfirmation, error) {
	log.Printf("GetConfiguration requested for %d keys", len(request.Key))
	return core.NewGetConfigurationConfirmation(nil), nil
}

func (h *ChargePointHandler) OnRemoteStartTransaction(request *core.RemoteStartTransactionRequest) (*core.RemoteStartTransactionConfirmation, error) {
	log.Printf("RemoteStartTransaction requested: idTag=%s connector=%d", request.IdTag, derefInt(request.ConnectorId))
	go h.startTransaction(request)
	return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

func (h *ChargePointHandler) OnRemoteStopTransaction(request *core.RemoteStopTransactionRequest) (*core.RemoteStopTransactionConfirmation, error) {
	log.Printf("RemoteStopTransaction requested: transactionID=%d", request.TransactionId)
	go h.stopTransaction(request.TransactionId, core.ReasonRemote)
	return core.NewRemoteStopTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

func (h *ChargePointHandler) OnReset(request *core.ResetRequest) (*core.ResetConfirmation, error) {
	log.Printf("Reset requested: type=%s", request.Type)
	return core.NewResetConfirmation(core.ResetStatusAccepted), nil
}

func (h *ChargePointHandler) OnUnlockConnector(request *core.UnlockConnectorRequest) (*core.UnlockConnectorConfirmation, error) {
	log.Printf("UnlockConnector requested: connector=%d", request.ConnectorId)
	return core.NewUnlockConnectorConfirmation(core.UnlockStatusUnlocked), nil
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func main() {
	cp := ocpp16.NewChargePoint("CP-001", nil, nil)
	handler := NewChargePointHandler(cp)
	cp.SetCoreHandler(handler)

	if err := cp.Start("ws://localhost:8887"); err != nil {
		log.Fatalf("failed to connect to CS: %v", err)
	}
	log.Println("Connected to central system")

	bootConf, err := cp.BootNotification("GoOCPP-Charger", "OCPP-Go-Demo")
	if err != nil {
		log.Fatalf("BootNotification failed: %v", err)
	}
	currentTime := "n/a"
	if bootConf.CurrentTime != nil {
		currentTime = bootConf.CurrentTime.FormatTimestamp()
	}
	fmt.Printf("BootNotification confirmed: status=%s interval=%d currentTime=%s\n", bootConf.Status, bootConf.Interval, currentTime)

	if bootConf.Interval > 0 {
		interval := time.Duration(bootConf.Interval) * time.Second
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				if _, err := cp.Heartbeat(); err != nil {
					log.Printf("heartbeat failed: %v", err)
				}
			}
		}()
	}

	select {} // keep running
}

func (h *ChargePointHandler) startTransaction(request *core.RemoteStartTransactionRequest) {
	connector := derefInt(request.ConnectorId)
	if connector == 0 {
		connector = 1
	}
	meterStart := int(time.Now().Unix() % 1000)
	timestamp := types.NewDateTime(time.Now())

	conf, err := h.cp.StartTransaction(connector, request.IdTag, meterStart, timestamp)
	if err != nil {
		log.Printf("start transaction failed: %v", err)
		return
	}

	h.mu.Lock()
	h.activeTxID = conf.TransactionId
	h.activeConnector = connector
	h.activeIdTag = request.IdTag
	h.meterStart = meterStart
	h.mu.Unlock()

	log.Printf("Started transaction %d on connector %d for idTag=%s", conf.TransactionId, connector, request.IdTag)
}

func (h *ChargePointHandler) stopTransaction(transactionID int, reason core.Reason) {
	h.mu.Lock()
	activeTxID := h.activeTxID
	connector := h.activeConnector
	idTag := h.activeIdTag
	meterStart := h.meterStart
	h.activeTxID = 0
	h.activeConnector = 0
	h.activeIdTag = ""
	h.meterStart = 0
	h.mu.Unlock()

	if transactionID == 0 {
		transactionID = activeTxID
	}
	if transactionID == 0 {
		log.Printf("no active transaction to stop")
		return
	}

	meterStop := meterStart + 100
	timestamp := types.NewDateTime(time.Now())

	_, err := h.cp.StopTransaction(meterStop, timestamp, transactionID, func(req *core.StopTransactionRequest) {
		if idTag != "" {
			req.IdTag = idTag
		}
		if reason != "" {
			req.Reason = reason
		}
	})
	if err != nil {
		log.Printf("stop transaction %d failed: %v", transactionID, err)
		return
	}

	log.Printf("Stopped transaction %d on connector %d (reason=%s)", transactionID, connector, reason)
}
