package main

import (
	"fmt"
	"log"
	"time"

	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
)

type ChargePointHandler struct{}

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
	return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

func (h *ChargePointHandler) OnRemoteStopTransaction(request *core.RemoteStopTransactionRequest) (*core.RemoteStopTransactionConfirmation, error) {
	log.Printf("RemoteStopTransaction requested: transactionID=%d", request.TransactionId)
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
	handler := &ChargePointHandler{}
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
