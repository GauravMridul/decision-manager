package decision_manager_models

import (
	"database/sql"
	"time"
)

// QueryObjectRelationshipMap represents the mapping between query objects and their relations
type QueryObjectRelationshipMap struct {
	ID               int64          `json:"id" gorm:"primaryKey;autoIncrement;type:bigint"`
	QueryObject      string         `json:"query_object" gorm:"type:varchar(255);not null"`
	QueryRelation    string         `json:"query_relation" gorm:"type:varchar(255);not null"`
	AdditionalFields sql.NullString `json:"additional_fields" gorm:"type:text"`
	CreatedDate      time.Time      `json:"created_date" gorm:"type:timestamp;not null"`
	CreatedBy        string         `json:"created_by" gorm:"type:varchar(255);not null"`
	ModifiedDate     time.Time      `json:"modified_date" gorm:"type:timestamp"`
	ModifiedBy       string         `json:"modified_by" gorm:"type:varchar(255)"`
	IsDeleted        bool           `json:"is_deleted" gorm:"default:false"`
}

// TableName specifies the table name for QueryObjectRelationshipMap
func (QueryObjectRelationshipMap) TableName() string {
	return "query_object_relationship_map"
}
