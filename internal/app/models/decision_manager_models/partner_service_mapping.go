package decision_manager_models

import (
	"time"
)

// PartnerServiceMapping represents a table that maps a partner to a Service Sequence String
type PartnerServiceMapping struct {
	ID                      int64     `json:"id" gorm:"primaryKey;autoIncrement;type:bigint"`
	Name                    string    `json:"name" gorm:"type:varchar(255);not null"`
	PartnerName             string    `json:"partner_name" gorm:"type:varchar(255);not null"`
	ProgramType             string    `json:"program_type" gorm:"type:varchar(255);default:''"`
	BusinessType            string    `json:"business_type" gorm:"type:varchar(255);default:''"`
	SourcingProgram         string    `json:"sourcing_program" gorm:"type:varchar(255);default:''"`
	LoanCategory            string    `json:"loan_category" gorm:"type:varchar(255);default:''"`
	CustomerType            string    `json:"customer_type" gorm:"type:varchar(255);default:''"`
	ProductLine             string    `json:"product_line" gorm:"type:varchar(255);default:''"`
	SalesChannelPartnerName string    `json:"sales_channel_partner_name" gorm:"type:varchar(255);default:''"`
	SourcingChannel         string    `json:"sourcing_channel" gorm:"type:varchar(255);default:''"`
	NameOfConsolidator      string    `json:"name_of_consolidator" gorm:"type:varchar(255);default:''"`
	Stage                   string    `json:"stage" gorm:"type:varchar(255);not null"`
	ServiceSequenceString   string    `json:"service_sequence_string" gorm:"type:varchar(255);not null"`
	CreatedDate             time.Time `json:"created_date" gorm:"type:timestamp;not null"`
	CreatedBy               string    `json:"created_by" gorm:"type:varchar(255);not null"`
	ModifiedDate            time.Time `json:"modified_date" gorm:"type:timestamp"`
	ModifiedBy              string    `json:"modified_by" gorm:"type:varchar(255)"`
	IsDeleted               bool      `json:"is_deleted" gorm:"default:false"`
}

type PartnerServiceMappingResponse struct {
	ID                    int64  `json:"id" gorm:"primaryKey;autoIncrement;type:bigint"`
	Name                  string `json:"name" gorm:"type:varchar(255);not null"`
	ServiceSequenceString string `json:"service_sequence_string" gorm:"type:varchar(255);not null"`
}

func (PartnerServiceMapping) TableName() string {
	return "partner_service_mapping"
}

func (PartnerServiceMappingResponse) TableName() string {
	return "partner_service_mapping"
}
