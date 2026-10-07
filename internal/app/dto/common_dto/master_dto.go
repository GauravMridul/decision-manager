package common_dto

import (
	"decision-manager/internal/app/dto/request_dto/external_service_adapter_request_dto"
	"decision-manager/internal/app/utility"
	"time"
)

// MasterDTO is the main structure for sequence execution
type MasterDTO struct {
	EsaRequestBody  *external_service_adapter_request_dto.ExternalServiceAdapterRequest `json:"esa_request_body"`
	EsaResponseBody interface{}                                                         `json:"esa_response_body"`

	SFCompositeSubRequestArray    []utility.SFRequest               `json:"sfCompositeSubRequestArray"`
	SFCompositeResponse           map[string]map[string]interface{} `json:"sfCompositeResponse"`
	TriggerDecisionStartTime      time.Time                         `json:"triggerDecisionStartTime"`
	TriggerDecisionEndTime        time.Time                         `json:"triggerDecisionEndTime"`
	TriggerDecisionTime           int64                             `json:"triggerDecisionTime"`
	EsaExecutionStartTime         time.Time                         `json:"esaExecutionStartTime"`
	EsaExecutionEndTime           time.Time                         `json:"esaExecutionEndTime"`
	EsaExecutionTime              int64                             `json:"esaExecutionTime"`
	SFCompositeExecutionStartTime time.Time                         `json:"sfCompositeExecutionStartTime"`
	SFCompositeExecutionEndTime   time.Time                         `json:"sfCompositeExecutionEndTime"`
	SFCompositeExecutionTime      int64                             `json:"sfCompositeExecutionTime"`
}

// DecisionManagerCacheConfig defines the cache configuration specific to Decision Manager
type DecisionManagerCacheConfig struct {
	Host                       string        `json:"host" mapstructure:"host"`
	Port                       int           `json:"port" mapstructure:"port"`
	Password                   string        `json:"password" mapstructure:"password"`
	Database                   int           `json:"database" mapstructure:"database"`
	PoolSize                   int           `json:"pool_size" mapstructure:"pool_size"`
	MinIdleConns               int           `json:"min_idle_conns" mapstructure:"min_idle_conns"`
	MaxRetries                 int           `json:"max_retries" mapstructure:"max_retries"`
	DialTimeout                time.Duration `json:"dial_timeout" mapstructure:"dial_timeout"`
	ReadTimeout                time.Duration `json:"read_timeout" mapstructure:"read_timeout"`
	WriteTimeout               time.Duration `json:"write_timeout" mapstructure:"write_timeout"`
	PoolTimeout                time.Duration `json:"pool_timeout" mapstructure:"pool_timeout"`
	DefaultTTL                 time.Duration `json:"default_ttl" mapstructure:"default_ttl"`
	PartnerServiceMappingTTL   time.Duration `json:"partner_service_mapping_ttl" mapstructure:"partner_service_mapping_ttl"`
	QueryObjectTTL             time.Duration `json:"query_object_ttl" mapstructure:"query_object_ttl"`
	ServiceSFDCFieldMappingTTL time.Duration `json:"service_sfdc_field_mapping_ttl" mapstructure:"service_sfdc_field_mapping_ttl"`
	Enabled                    bool          `json:"enabled" mapstructure:"enabled"`
	Compression                bool          `json:"compression" mapstructure:"compression"`
}
