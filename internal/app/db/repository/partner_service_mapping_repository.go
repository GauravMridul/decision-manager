package repository

import (
	"context"
	"decision-manager/internal/app/db"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/internal/app/models/decision_manager_models"
	"errors"
)

const partnerServiceMappingSpecificityOrderClause = "CASE WHEN partner_name = '' THEN 1 ELSE 0 END, CASE WHEN program_type = '' THEN 1 ELSE 0 END, CASE WHEN business_type = '' THEN 1 ELSE 0 END, CASE WHEN sourcing_program = '' THEN 1 ELSE 0 END, CASE WHEN loan_category = '' THEN 1 ELSE 0 END, CASE WHEN customer_type = '' THEN 1 ELSE 0 END, CASE WHEN product_line = '' THEN 1 ELSE 0 END, CASE WHEN sales_channel_partner_name = '' THEN 1 ELSE 0 END, CASE WHEN sourcing_channel = '' THEN 1 ELSE 0 END, CASE WHEN name_of_consolidator = '' THEN 1 ELSE 0 END"

type IPartnerServiceMappingInterface interface {
	FindByExcludingIdAndName(ctx context.Context, partnerName string, programType string, businessType string, sourcingProgram string, loanCategory string, customerType string, productLine string, salesChannelPartnerName string, sourcingChannel string, nameOfConsolidator string, stage string) (*decision_manager_models.PartnerServiceMappingResponse, error)
}

type PartnerServiceMappingRepositoryImpl struct {
	DBService db.DBService
}

func NewPartnerServiceMappingRepositoryImpl() *PartnerServiceMappingRepositoryImpl {
	repo := &PartnerServiceMappingRepositoryImpl{
		DBService: db.DBService{},
	}
	return repo
}

func (r *PartnerServiceMappingRepositoryImpl) FindByExcludingIdAndName(ctx context.Context, partnerName string, programType string, businessType string, sourcingProgram string, loanCategory string, customerType string, productLine string, salesChannelPartnerName string, sourcingChannel string, nameOfConsolidator string, stage string) (*decision_manager_models.PartnerServiceMappingResponse, error) {
	log := commoninit.GetLogger(ctx)
	log.Info("Inside FindByPartnerNameProgramTypeStage method")

	// var serviceConfigurations []*esa_models.ServiceConfiguration

	var PartnerServiceMappingList *decision_manager_models.PartnerServiceMappingResponse

	dbConnection := r.DBService.GetDB()
	if dbConnection == nil {
		log.Warn("DB connection is Null")
		return PartnerServiceMappingList, errors.New("db connection is null")
	}

	// ORDER BY specificity: rows with a non-empty (specific) value sort before rows with an
	// empty-string catch-all. NULLS LAST has no effect on empty strings in PostgreSQL, so
	// we use CASE expressions to rank empty strings last explicitly.
	result := dbConnection.Select("id", "name", "service_sequence_string").Where(
		"(partner_name = ? OR partner_name='') AND (program_type = ? OR program_type='') AND (business_type = ? OR business_type='') AND (sourcing_program = ? OR sourcing_program='') AND (loan_category = ? OR loan_category='') AND (customer_type = ? OR customer_type='') AND (product_line = ? OR product_line='') AND (sales_channel_partner_name = ? OR sales_channel_partner_name='') AND (sourcing_channel = ? OR sourcing_channel='') AND (name_of_consolidator = ? OR name_of_consolidator='') AND stage = ? AND is_deleted = false",
		partnerName, programType, businessType, sourcingProgram, loanCategory, customerType, productLine, salesChannelPartnerName, sourcingChannel, nameOfConsolidator, stage).
		Order(partnerServiceMappingSpecificityOrderClause).
		Limit(1).Find(&PartnerServiceMappingList)

	log.Info("Partner Service Mapping List PartnerServiceMappingList: ", PartnerServiceMappingList)

	if result.Error != nil {
		log.Errorw("Error fetching partner service mapping", "error", result.Error)
		return nil, result.Error
	}

	return PartnerServiceMappingList, nil

}
