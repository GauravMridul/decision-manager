package decision_manager_log_models

import (
	"decision-manager/internal/app/dto/request_dto/external_service_adapter_request_dto"
	"decision-manager/internal/app/types"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type DecisionManagerLog struct {
	ID                         primitive.ObjectID                                                  `bson:"_id,omitempty" json:"id,omitempty"`
	EsaRequestBody             *external_service_adapter_request_dto.ExternalServiceAdapterRequest `bson:"esaRequestBody" json:"esaRequestBody"`
	EsaResponseBody            interface{}                                                         `bson:"esaResponseBody" json:"esaResponseBody"`
	SFCompositeSubRequestArray []types.SFRequest                                                   `bson:"sfCompositeSubRequestArray" json:"sfCompositeSubRequestArray"`
	SFCompositeResponse        map[string]map[string]interface{}                                   `bson:"sfCompositeResponse" json:"sfCompositeResponse"`
	TriggerDecisionStartTime   time.Time                                                           `bson:"triggerDecisionStartTime" json:"triggerDecisionStartTime"`
	TriggerDecisionEndTime     time.Time                                                           `bson:"triggerDecisionEndTime" json:"triggerDecisionEndTime"`
	TriggerDecisionTime        int64                                                               `bson:"triggerDecisionTime" json:"triggerDecisionTime"`

	EsaExecutionStartTime         time.Time `bson:"esaExecutionStartTime" json:"esaExecutionStartTime"`
	EsaExecutionEndTime           time.Time `bson:"esaExecutionEndTime" json:"esaExecutionEndTime"`
	EsaExecutionTime              int64     `bson:"esaExecutionTime" json:"esaExecutionTime"`
	SFCompositeExecutionStartTime time.Time `bson:"sfCompositeExecutionStartTime" json:"sfCompositeExecutionStartTime"`
	SFCompositeExecutionEndTime   time.Time `bson:"sfCompositeExecutionEndTime" json:"sfCompositeExecutionEndTime"`
	SFCompositeExecutionTime      int64     `bson:"sfCompositeExecutionTime" json:"sfCompositeExecutionTime"`
	CreatedAt                     int64     `bson:"createdAt" json:"createdAt"`
	IsDeleted                     bool      `bson:"isDeleted" json:"isDeleted"`
	UpdatedAt                     int64     `bson:"updatedAt" json:"updatedAt"`

	// Additional fields can be added dynamically using the ExtraFields
	ExtraFields map[string]interface{} `bson:",inline" json:"-"`
}

// NewDecisionManagerLog creates a new DecisionManagerLog with default values
func NewDecisionManagerLog() *DecisionManagerLog {
	now := time.Now().Unix()
	return &DecisionManagerLog{
		EsaRequestBody:                &external_service_adapter_request_dto.ExternalServiceAdapterRequest{},
		EsaResponseBody:               nil,
		SFCompositeSubRequestArray:    []types.SFRequest{},
		SFCompositeResponse:           make(map[string]map[string]interface{}),
		TriggerDecisionStartTime:      time.Time{},
		TriggerDecisionEndTime:        time.Time{},
		TriggerDecisionTime:           0,
		EsaExecutionStartTime:         time.Time{},
		EsaExecutionEndTime:           time.Time{},
		EsaExecutionTime:              0,
		SFCompositeExecutionStartTime: time.Time{},
		SFCompositeExecutionEndTime:   time.Time{},
		SFCompositeExecutionTime:      0,
		IsDeleted:                     false,
		CreatedAt:                     now,
		UpdatedAt:                     now,
		ExtraFields:                   make(map[string]interface{}),
	}
}

// SetField sets a field in the EsaLog, either in the predefined fields or in ExtraFields
func (log *DecisionManagerLog) SetField(key string, value interface{}) {
	if log.ExtraFields == nil {
		log.ExtraFields = make(map[string]interface{})
	}
	log.ExtraFields[key] = value
}

// GetField gets a field value from either predefined fields or ExtraFields
func (log *DecisionManagerLog) GetField(key string) interface{} {
	if log.ExtraFields == nil {
		return nil
	}
	if value, ok := log.ExtraFields[key]; ok {
		return value
	}

	// If field not found in structure
	return nil
}
