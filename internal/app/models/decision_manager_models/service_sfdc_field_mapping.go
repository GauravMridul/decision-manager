package decision_manager_models

import (
	"time"

	"gorm.io/datatypes"
)

// ServiceSfdcFieldMapping represents a table that maps a service name to a JSON configuration of Salesforce objects and fields to update
type ServiceSfdcFieldMapping struct {
	ID           int64          `json:"id" gorm:"primaryKey;autoIncrement;type:bigint"`
	ServiceName  string         `json:"service_name" gorm:"type:varchar(255);not null"`
	RequestBody  datatypes.JSON `json:"request_body" gorm:"type:jsonb;default:'{}'"`
	CreatedDate  time.Time      `json:"created_date" gorm:"type:timestamp;not null"`
	CreatedBy    string         `json:"created_by" gorm:"type:varchar(255);not null"`
	ModifiedDate time.Time      `json:"modified_date" gorm:"type:timestamp"`
	ModifiedBy   string         `json:"modified_by" gorm:"type:varchar(255)"`
	IsDeleted    bool           `json:"is_deleted" gorm:"default:false"`
}

type ServiceSfdcFieldMappingResponse struct {
	ID          int64          `json:"id"`
	ServiceName string         `json:"service_name"`
	RequestBody datatypes.JSON `json:"request_body"`
}

// specifies the table name for ServiceSfdcFieldMapping
func (ServiceSfdcFieldMapping) TableName() string {
	return "service_sfdc_field_mapping"
}

// specifies the table name for ServiceSfdcFieldMappingResponse
func (ServiceSfdcFieldMappingResponse) TableName() string {
	return "service_sfdc_field_mapping"
}
