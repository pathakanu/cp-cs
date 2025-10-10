package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/certificates"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/extendedtriggermessage"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/firmware"
	localauth "github.com/lorenzodonini/ocpp-go/ocpp1.6/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/logging"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/remotetrigger"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/reservation"
	securefirmware "github.com/lorenzodonini/ocpp-go/ocpp1.6/securefirmware"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/security"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/smartcharging"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/lorenzodonini/ocpp-go/ws"
)

func TestHandleChargePointsEmpty(t *testing.T) {
	app := &CSApp{
		chargePoints: map[string]*chargePointState{},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/chargepoints", nil)
	rec := httptest.NewRecorder()

	app.handleChargePoints(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var payload []chargePointView
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("failed decoding response: %v", err)
	}
	if len(payload) != 0 {
		t.Fatalf("expected empty list, got %+v", payload)
	}
}

func TestHandleRemoteStartSuccess(t *testing.T) {
	mock := &stubCentralSystem{
		remoteStartFn: func(clientID string, callback func(*core.RemoteStartTransactionConfirmation, error), idTag string, props ...func(*core.RemoteStartTransactionRequest)) error {
			if clientID != "CP-001" {
				t.Fatalf("unexpected charge point: %s", clientID)
			}
			if idTag != "TAG-123" {
				t.Fatalf("unexpected idTag: %s", idTag)
			}
			go callback(core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil)
			return nil
		},
	}

	app := &CSApp{
		central: mock,
		chargePoints: map[string]*chargePointState{
			"CP-001": {ID: "CP-001", Connected: true},
		},
	}

	body := `{"chargePointId":"CP-001","idTag":"TAG-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/remote-start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.handleRemoteStart(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var payload map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("failed decoding response: %v", err)
	}
	if payload["status"] != string(types.RemoteStartStopStatusAccepted) {
		t.Fatalf("unexpected status %+v", payload)
	}
}

func TestHandleRemoteStartRejectsWhenDisconnected(t *testing.T) {
	app := &CSApp{
		central:      &stubCentralSystem{},
		chargePoints: map[string]*chargePointState{},
	}

	body := `{"chargePointId":"CP-404","idTag":"TAG"}`
	req := httptest.NewRequest(http.MethodPost, "/api/remote-start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.handleRemoteStart(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRemoteStartPropagatesError(t *testing.T) {
	mock := &stubCentralSystem{
		remoteStartFn: func(clientID string, callback func(*core.RemoteStartTransactionConfirmation, error), idTag string, props ...func(*core.RemoteStartTransactionRequest)) error {
			return errors.New("not connected")
		},
	}

	app := &CSApp{
		central: mock,
		chargePoints: map[string]*chargePointState{
			"CP-001": {ID: "CP-001", Connected: true},
		},
	}

	body := `{"chargePointId":"CP-001","idTag":"TAG-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/remote-start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.handleRemoteStart(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRemoteStartCallbackError(t *testing.T) {
	mock := &stubCentralSystem{
		remoteStartFn: func(clientID string, callback func(*core.RemoteStartTransactionConfirmation, error), idTag string, props ...func(*core.RemoteStartTransactionRequest)) error {
			go callback(nil, errors.New("cp error"))
			return nil
		},
	}

	app := &CSApp{
		central: mock,
		chargePoints: map[string]*chargePointState{
			"CP-001": {ID: "CP-001", Connected: true},
		},
	}

	body := `{"chargePointId":"CP-001","idTag":"TAG-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/remote-start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	app.handleRemoteStart(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestChargePointViewSorting(t *testing.T) {
	app := &CSApp{
		chargePoints: map[string]*chargePointState{
			"B": {
				ID:        "B",
				Connected: true,
			},
			"A": {
				ID:        "A",
				Connected: true,
				LastBoot:  time.Now(),
			},
		},
	}

	list := app.listChargePoints()
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}
	if list[0].ID != "A" || list[1].ID != "B" {
		t.Fatalf("expected alphabetical order: %+v", list)
	}
}

// stubCentralSystem is a lightweight implementation of the ocpp central system interface
// that lets tests control the asynchronous callbacks exercised by the HTTP handlers.
type stubCentralSystem struct {
	remoteStartFn func(string, func(*core.RemoteStartTransactionConfirmation, error), string, ...func(*core.RemoteStartTransactionRequest)) error
	remoteStopFn  func(string, func(*core.RemoteStopTransactionConfirmation, error), int, ...func(*core.RemoteStopTransactionRequest)) error
	resetFn       func(string, func(*core.ResetConfirmation, error), core.ResetType, ...func(*core.ResetRequest)) error
	unlockFn      func(string, func(*core.UnlockConnectorConfirmation, error), int, ...func(*core.UnlockConnectorRequest)) error
}

func (s *stubCentralSystem) ChangeAvailability(string, func(*core.ChangeAvailabilityConfirmation, error), int, core.AvailabilityType, ...func(*core.ChangeAvailabilityRequest)) error {
	return nil
}

func (s *stubCentralSystem) ChangeConfiguration(string, func(*core.ChangeConfigurationConfirmation, error), string, string, ...func(*core.ChangeConfigurationRequest)) error {
	return nil
}

func (s *stubCentralSystem) ClearCache(string, func(*core.ClearCacheConfirmation, error), ...func(*core.ClearCacheRequest)) error {
	return nil
}

func (s *stubCentralSystem) DataTransfer(string, func(*core.DataTransferConfirmation, error), string, ...func(*core.DataTransferRequest)) error {
	return nil
}

func (s *stubCentralSystem) GetConfiguration(string, func(*core.GetConfigurationConfirmation, error), []string, ...func(*core.GetConfigurationRequest)) error {
	return nil
}

func (s *stubCentralSystem) RemoteStartTransaction(clientId string, callback func(*core.RemoteStartTransactionConfirmation, error), idTag string, props ...func(*core.RemoteStartTransactionRequest)) error {
	if s.remoteStartFn != nil {
		return s.remoteStartFn(clientId, callback, idTag, props...)
	}
	return nil
}

func (s *stubCentralSystem) RemoteStopTransaction(clientId string, callback func(*core.RemoteStopTransactionConfirmation, error), transactionId int, props ...func(*core.RemoteStopTransactionRequest)) error {
	if s.remoteStopFn != nil {
		return s.remoteStopFn(clientId, callback, transactionId, props...)
	}
	return nil
}

func (s *stubCentralSystem) Reset(clientId string, callback func(*core.ResetConfirmation, error), resetType core.ResetType, props ...func(*core.ResetRequest)) error {
	if s.resetFn != nil {
		return s.resetFn(clientId, callback, resetType, props...)
	}
	return nil
}

func (s *stubCentralSystem) UnlockConnector(clientId string, callback func(*core.UnlockConnectorConfirmation, error), connectorId int, props ...func(*core.UnlockConnectorRequest)) error {
	if s.unlockFn != nil {
		return s.unlockFn(clientId, callback, connectorId, props...)
	}
	return nil
}

func (s *stubCentralSystem) GetLocalListVersion(string, func(*localauth.GetLocalListVersionConfirmation, error), ...func(*localauth.GetLocalListVersionRequest)) error {
	return nil
}

func (s *stubCentralSystem) SendLocalList(string, func(*localauth.SendLocalListConfirmation, error), int, localauth.UpdateType, ...func(*localauth.SendLocalListRequest)) error {
	return nil
}

func (s *stubCentralSystem) GetDiagnostics(string, func(*firmware.GetDiagnosticsConfirmation, error), string, ...func(*firmware.GetDiagnosticsRequest)) error {
	return nil
}

func (s *stubCentralSystem) UpdateFirmware(string, func(*firmware.UpdateFirmwareConfirmation, error), string, *types.DateTime, ...func(*firmware.UpdateFirmwareRequest)) error {
	return nil
}

func (s *stubCentralSystem) ReserveNow(string, func(*reservation.ReserveNowConfirmation, error), int, *types.DateTime, string, int, ...func(*reservation.ReserveNowRequest)) error {
	return nil
}

func (s *stubCentralSystem) CancelReservation(string, func(*reservation.CancelReservationConfirmation, error), int, ...func(*reservation.CancelReservationRequest)) error {
	return nil
}

func (s *stubCentralSystem) TriggerMessage(string, func(*remotetrigger.TriggerMessageConfirmation, error), remotetrigger.MessageTrigger, ...func(*remotetrigger.TriggerMessageRequest)) error {
	return nil
}

func (s *stubCentralSystem) SetChargingProfile(string, func(*smartcharging.SetChargingProfileConfirmation, error), int, *types.ChargingProfile, ...func(*smartcharging.SetChargingProfileRequest)) error {
	return nil
}

func (s *stubCentralSystem) ClearChargingProfile(string, func(*smartcharging.ClearChargingProfileConfirmation, error), ...func(*smartcharging.ClearChargingProfileRequest)) error {
	return nil
}

func (s *stubCentralSystem) GetCompositeSchedule(string, func(*smartcharging.GetCompositeScheduleConfirmation, error), int, int, ...func(*smartcharging.GetCompositeScheduleRequest)) error {
	return nil
}

func (s *stubCentralSystem) TriggerMessageExtended(string, func(*extendedtriggermessage.ExtendedTriggerMessageResponse, error), extendedtriggermessage.ExtendedTriggerMessageType, ...func(*extendedtriggermessage.ExtendedTriggerMessageRequest)) error {
	return nil
}

func (s *stubCentralSystem) CertificateSigned(string, func(*security.CertificateSignedResponse, error), string, ...func(*security.CertificateSignedRequest)) error {
	return nil
}

func (s *stubCentralSystem) InstallCertificate(string, func(*certificates.InstallCertificateResponse, error), types.CertificateUse, string, ...func(*certificates.InstallCertificateRequest)) error {
	return nil
}

func (s *stubCentralSystem) GetInstalledCertificateIds(string, func(*certificates.GetInstalledCertificateIdsResponse, error), types.CertificateUse, ...func(*certificates.GetInstalledCertificateIdsRequest)) error {
	return nil
}

func (s *stubCentralSystem) DeleteCertificate(string, func(*certificates.DeleteCertificateResponse, error), types.CertificateHashData, ...func(*certificates.DeleteCertificateRequest)) error {
	return nil
}

func (s *stubCentralSystem) GetLog(string, func(*logging.GetLogResponse, error), logging.LogType, int, logging.LogParameters, ...func(*logging.GetLogRequest)) error {
	return nil
}

func (s *stubCentralSystem) SignedUpdateFirmware(string, func(*securefirmware.SignedUpdateFirmwareResponse, error), int, securefirmware.Firmware, ...func(*securefirmware.SignedUpdateFirmwareRequest)) error {
	return nil
}

func (s *stubCentralSystem) SetCoreHandler(core.CentralSystemHandler) {}

func (s *stubCentralSystem) SetLocalAuthListHandler(localauth.CentralSystemHandler) {}

func (s *stubCentralSystem) SetFirmwareManagementHandler(firmware.CentralSystemHandler) {}

func (s *stubCentralSystem) SetReservationHandler(reservation.CentralSystemHandler) {}

func (s *stubCentralSystem) SetRemoteTriggerHandler(remotetrigger.CentralSystemHandler) {}

func (s *stubCentralSystem) SetSmartChargingHandler(smartcharging.CentralSystemHandler) {}

func (s *stubCentralSystem) SetSecurityHandler(security.CentralSystemHandler) {}

func (s *stubCentralSystem) SetLogHandler(logging.CentralSystemHandler) {}

func (s *stubCentralSystem) SetSecureFirmwareHandler(securefirmware.CentralSystemHandler) {}

func (s *stubCentralSystem) SetNewChargingStationValidationHandler(ws.CheckClientHandler) {}

func (s *stubCentralSystem) SetNewChargePointHandler(ocpp16.ChargePointConnectionHandler) {}

func (s *stubCentralSystem) SetChargePointDisconnectedHandler(ocpp16.ChargePointConnectionHandler) {}

func (s *stubCentralSystem) SendRequestAsync(string, ocpp.Request, func(ocpp.Response, error)) error {
	return nil
}

func (s *stubCentralSystem) Start(int, string) {}

func (s *stubCentralSystem) Stop() {}

func (s *stubCentralSystem) Errors() <-chan error { return nil }

// Ensure stubCentralSystem implements the ocpp interface.
var _ ocpp16.CentralSystem = (*stubCentralSystem)(nil)
