package utility

import (
	"context"

	commoninit "decision-manager/internal/app/init"

	"github.com/google/uuid"

	"github.com/go-playground/validator/v10"
)

// RequestValidatorUtil :
type IRequestValidatorUtil interface {
	IsValidRequest(ctx context.Context, leadId string, applicationId string, leadSourceId string) bool
	IsValidUUID(uuid string) bool
}

// RequestValidator :
type RequestValidator struct {
}

// NewRequestValidator :
func NewRequestValidator() *RequestValidator {
	return &RequestValidator{}
}

// IsValidRequest : ctx optional; when provided, request-context enriched logger is used.
func (u RequestValidator) IsValidRequest(ctx context.Context, leadId string, applicationId string, leadSourceId string) bool {
	log := commoninit.GetLogger(ctx)
	methodName := "IsValidRequest:"
	log.Info(methodName, " Validating the request for lead ", "leadId ", leadId, " applicationId ", applicationId, " and leadSourceId ", leadSourceId)
	if !(leadId == "") {
		return false
	}
	if !(applicationId == "") {
		return false
	}
	if !(leadSourceId == "") {
		return false
	}
	return true
}

func IsValidUUID(u string) bool {
	parsedUUID, err := uuid.Parse(u)
	return err == nil && parsedUUID != uuid.Nil
}

// Need to modified as per the requirement
func NewValidator() *validator.Validate {
	requestValidator := validator.New()
	err := requestValidator.RegisterValidation("statusEnum", IsValidStatusEnumValue)
	if err != nil {
		commoninit.GetLogger().Errorf("Error while registering custom validator func IsValidStatusEnumValue %s\n", err.Error())
	}
	err = requestValidator.RegisterValidation("requestContext", IsValidRequestContext)
	if err != nil {
		commoninit.GetLogger().Errorf("Error while registering custom validator func IsValidRequestContext %s\n", err.Error())
	}
	return requestValidator
}

// Need to modified as per the requirement
func IsValidStatusEnumValue(fl validator.FieldLevel) bool {
	// status := fl.Field().Int()
	// if common_enums.Status(status).String() == constants.UnknownEnumValue {
	// 	return false
	// }
	return true
}

// Need to modified as per the requirement
func IsValidRequestContext(fl validator.FieldLevel) bool {
	// requestContext := fl.Field().String()
	// if common_enums.Parse(requestContext) == 0 {
	// 	return false
	// }
	return true
}
