package repository

import (
	"context"
	"decision-manager/internal/app/db"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/internal/app/models/decision_manager_models"
	"errors"
)

type IServiceSfdcFieldMappingRepository interface {
	FindByServiceIdArray(ctx context.Context, serviceIds []int) ([]*decision_manager_models.ServiceSfdcFieldMappingResponse, error)
	FindAll(ctx context.Context) ([]*decision_manager_models.ServiceSfdcFieldMappingResponse, error)
	FindByServiceNameArray(ctx context.Context, serviceNameArray []interface{}) ([]*decision_manager_models.ServiceSfdcFieldMappingResponse, error)
}

type ServiceSfdcFieldMappingRepositoryImpl struct {
	DBService db.DBService
}

func NewServiceSfdcFieldMappingRepositoryImpl() *ServiceSfdcFieldMappingRepositoryImpl {
	repo := &ServiceSfdcFieldMappingRepositoryImpl{
		DBService: db.DBService{},
	}
	return repo
}

func (r *ServiceSfdcFieldMappingRepositoryImpl) FindByServiceIdArray(ctx context.Context, serviceIds []int) ([]*decision_manager_models.ServiceSfdcFieldMappingResponse, error) {
	log := commoninit.GetLogger(ctx)
	log.Info("Inside FindByServiceIdArray method")

	// var serviceConfigurations []*esa_models.ServiceConfiguration

	var ServiceSfdcFieldMappingList []*decision_manager_models.ServiceSfdcFieldMappingResponse

	dbConnection := r.DBService.GetDB()
	if dbConnection == nil {
		log.Warn("DB connection is Null")
		return ServiceSfdcFieldMappingList, errors.New("db connection is null")
	}

	result := dbConnection.Select("id", "service_name", "request_body").Where("id IN (?) AND is_deleted = false", serviceIds).Find(&ServiceSfdcFieldMappingList)
	if result.Error != nil {
		log.Errorw("Error fetching service configurations", "error", result.Error)
		return nil, result.Error
	}

	return ServiceSfdcFieldMappingList, nil

}
func (r *ServiceSfdcFieldMappingRepositoryImpl) FindByServiceNameArray(ctx context.Context, serviceNameArray []interface{}) ([]*decision_manager_models.ServiceSfdcFieldMappingResponse, error) {
	log := commoninit.GetLogger(ctx)
	log.Info("Inside FindByServiceNameArray method")

	// var serviceConfigurations []*esa_models.ServiceConfiguration

	var ServiceSfdcFieldMappingList []*decision_manager_models.ServiceSfdcFieldMappingResponse

	dbConnection := r.DBService.GetDB()
	if dbConnection == nil {
		log.Warn("DB connection is Null")
		return ServiceSfdcFieldMappingList, errors.New("db connection is null")
	}

	result := dbConnection.Select("id", "service_name", "request_body").Where("service_name IN (?) AND is_deleted = false", serviceNameArray).Find(&ServiceSfdcFieldMappingList)
	if result.Error != nil {
		log.Errorw("Error fetching service configurations", "error", result.Error)
		return nil, result.Error
	}

	return ServiceSfdcFieldMappingList, nil

}

func (r *ServiceSfdcFieldMappingRepositoryImpl) FindAll(ctx context.Context) ([]*decision_manager_models.ServiceSfdcFieldMappingResponse, error) {
	log := commoninit.GetLogger(ctx)
	log.Info("Inside FindAll method")

	var ServiceSfdcFieldMappingList []*decision_manager_models.ServiceSfdcFieldMappingResponse

	dbConnection := r.DBService.GetDB()
	if dbConnection == nil {
		log.Warn("DB connection is Null")
		return ServiceSfdcFieldMappingList, errors.New("db connection is null")
	}

	result := dbConnection.Select("id", "service_name", "request_body").Where(" is_deleted = false").Find(&ServiceSfdcFieldMappingList)
	if result.Error != nil {
		log.Errorw("Error fetching service configurations", "error", result.Error)
		return nil, result.Error
	}

	return ServiceSfdcFieldMappingList, nil
}
