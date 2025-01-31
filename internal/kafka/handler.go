package kafka

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"gorm.io/gorm"

	"github.com/redhatinsights/payload-tracker-go/internal/config"
	l "github.com/redhatinsights/payload-tracker-go/internal/logging"
	models "github.com/redhatinsights/payload-tracker-go/internal/models/db"
	"github.com/redhatinsights/payload-tracker-go/internal/models/message"
	"github.com/redhatinsights/payload-tracker-go/internal/queries"
)

var (
	tableNames = []string{"service", "source", "status"}
)

type handler struct {
	db *gorm.DB
}

// OnMessage takes in each payload status message and processes it
func (this *handler) onMessage(ctx context.Context, msg *kafka.Message, cfg *config.TrackerConfig) {
	l.Log.Debug("Processing Payload Message ", msg.Value)

	payloadStatus := &message.PayloadStatusMessage{}
	sanitizedPayloadStatus := &models.PayloadStatuses{}

	if err := json.Unmarshal(msg.Value, payloadStatus); err != nil {
		// PROBE: Add probe here for error unmarshaling JSON
		l.Log.Error("ERROR: Unmarshaling Payload Status Event: ", err)
		return
	}

	// Validate RequestID
	if cfg.RequestConfig.ValidateRequestID {
		if len(payloadStatus.RequestID) > cfg.RequestConfig.ValidateRequestIDLength {
			l.Log.Errorf("ERROR: Payload {value} has invalid request_id length.")
			return
		}
	}

	// Sanitize the payload
	sanitizePayload(payloadStatus)

	// Check if request_id not in Payloads Table and update columns
	l.Log.Info("Sanitized Payload for DB ", payloadStatus)

	payloadDump, err := getPayload(this.db, payloadStatus.RequestID)
	if err != nil && err != gorm.ErrRecordNotFound {
		l.Log.Error("ERROR: Sanitizing payload failed")
		return
	}

	if (models.Payloads{}) == payloadDump {
		payload := createPayload(payloadStatus)

		result, newPayload := queries.CreatePayloadTableEntry(this.db, payload)
		if result.Error != nil {
			l.Log.Error("ERROR Payload table entry creation Failed: ", result.Error)
			return
		}
		sanitizedPayloadStatus.PayloadId = newPayload.Id
	} else {
		payloadsUpdate := createPayload(payloadStatus)

		result := queries.UpdatePayloadsTable(this.db, payloadsUpdate, payloadDump)
		if result.Error != nil {
			l.Log.Error("ERROR Payload table update failed: ", result.Error)
			return
		}
		sanitizedPayloadStatus.PayloadId = payloadDump.Id
	}

	// Check if service/source/status are in table
	// this section checks the subsiquent DB tables to see if the service_id, source_id, and status_id exist for the given message
	l.Log.Debug("Adding Status, Sources, and Services to sanitizedPayload")

	// Status & Service: Always defined in the message
	existingStatus := queries.GetStatusByName(this.db, payloadStatus.Status)
	existingService := queries.GetServiceByName(this.db, payloadStatus.Service)
	if (models.Statuses{}) == existingStatus {
		statusResult, newStatus := queries.CreateStatusTableEntry(this.db, payloadStatus.Status)
		if statusResult.Error != nil {
			l.Log.Error("Error Creating Statuses Table Entry ERROR: ", statusResult.Error)
			return
		}

		sanitizedPayloadStatus.Status = newStatus
	} else {
		sanitizedPayloadStatus.Status = existingStatus
	}

	if (models.Services{}) == existingService {
		serviceResult, newService := queries.CreateServiceTableEntry(this.db, payloadStatus.Service)
		if serviceResult.Error != nil {
			l.Log.Error("Error Creating Service Table Entry ERROR: ", serviceResult.Error)
			return
		}

		sanitizedPayloadStatus.Service = newService
	} else {
		sanitizedPayloadStatus.Service = existingService
	}

	// Sources
	if payloadStatus.Source != "" {
		existingSource := queries.GetSourceByName(this.db, payloadStatus.Source)
		if (models.Sources{}) == existingSource {
			result, newSource := queries.CreateSourceTableEntry(this.db, payloadStatus.Source)
			if result.Error != nil {
				l.Log.Error("Error Creating Sources Table Entry ERROR: ", result.Error)
				return
			}

			sanitizedPayloadStatus.Source = newSource
		} else {
			sanitizedPayloadStatus.Source = existingSource
		}
	}

	if payloadStatus.StatusMSG != "" {
		sanitizedPayloadStatus.StatusMsg = payloadStatus.StatusMSG
	}

	// Insert Date
	sanitizedPayloadStatus.Date = payloadStatus.Date.Time

	// Insert payload into DB
	result := queries.InsertPayloadStatus(this.db, sanitizedPayloadStatus)
	if result.Error != nil {
		l.Log.Debug("Failed to insert sanitized PayloadStatus with ERROR: ", result.Error)
		result = queries.InsertPayloadStatus(this.db, sanitizedPayloadStatus)
		if result.Error != nil {
			l.Log.Debug("Failed to re-insert sanitized PayloadStatus with ERROR: ", result.Error)
			result = queries.InsertPayloadStatus(this.db, sanitizedPayloadStatus)
			if result.Error != nil {
				l.Log.Error("Failed final attempt to re-insert PayloadStatus with ERROR: ", result.Error)
			}
		}
	}
}

func sanitizePayload(msg *message.PayloadStatusMessage) {
	// Set default fields to lowercase
	msg.Service = strings.ToLower(msg.Service)
	msg.Status = strings.ToLower(msg.Status)
	if msg.Source != "" {
		msg.Source = strings.ToLower((msg.Source))
	}
}

func getPayload(db *gorm.DB, request_id string) (results models.Payloads, err error) {
	return queries.GetPayloadByRequestId(db, request_id)
}

func createPayload(msg *message.PayloadStatusMessage) (table models.Payloads) {
	payloadTable := models.Payloads{
		Id:          msg.PayloadID,
		RequestId:   msg.RequestID,
		Account:     msg.Account,
		SystemId:    msg.SystemID,
		CreatedAt:   msg.Date.Time,
		InventoryId: msg.InventoryID,
	}

	return payloadTable
}
