package utility

import (
	"decision-manager/internal/app/constants"
	"strconv"
	"strings"

	commoninit "decision-manager/internal/app/init"
)

type IRedisKeyGenerator interface {
	GenerateRedisKeyForPartnerServiceMapping(partnerName string, programType string, businessType string, sourcingProgram string, loanCategory string, customerType string, productLine string, salesChannelPartnerName string, sourcingChannel string, nameOfConsolidator string, stage string) string
	GenerateRedisKeyForServiceSfdcFieldMapping(sequenceId int64) string
	GenerateRedisKeyForAllServiceSfdcFieldMapping() string
}

type RedisKeyGenerator struct {
}

func NewRedisKeyGenerator() *RedisKeyGenerator {
	return &RedisKeyGenerator{}
}

func (u RedisKeyGenerator) GenerateRedisKeyForPartnerServiceMapping(partnerName string, programType string, businessType string, sourcingProgram string, loanCategory string, customerType string, productLine string, salesChannelPartnerName string, sourcingChannel string, nameOfConsolidator string, stage string) string {
	env := commoninit.GetConfigString("Environment")
	var builder strings.Builder
	builder.Grow(len(constants.SERVICE_NAME_KEY) + len(env) + len("partnerServiceMapping") + 128)
	builder.WriteString(constants.SERVICE_NAME_KEY)
	builder.WriteString(env)
	builder.WriteString("partnerServiceMapping")
	builder.WriteString(constants.COLON_SEPERATOR)

	if len(partnerName) > 0 {
		builder.WriteString(partnerName)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(programType) > 0 {
		builder.WriteString(programType)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(businessType) > 0 {
		builder.WriteString(businessType)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(sourcingProgram) > 0 {
		builder.WriteString(sourcingProgram)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(loanCategory) > 0 {
		builder.WriteString(loanCategory)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(customerType) > 0 {
		builder.WriteString(customerType)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(productLine) > 0 {
		builder.WriteString(productLine)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(salesChannelPartnerName) > 0 {
		builder.WriteString(salesChannelPartnerName)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(sourcingChannel) > 0 {
		builder.WriteString(sourcingChannel)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(nameOfConsolidator) > 0 {
		builder.WriteString(nameOfConsolidator)
		builder.WriteString(constants.COLON_SEPERATOR)
	}
	if len(stage) > 0 {
		builder.WriteString(stage)
	}
	return builder.String()
}

func (u RedisKeyGenerator) GenerateRedisKeyForServiceSfdcFieldMapping(sequenceId int64) string {
	env := commoninit.GetConfigString("Environment")
	var key = constants.SERVICE_NAME_KEY + env + "serviceSfdcFieldMapping" + constants.COLON_SEPERATOR + strconv.FormatInt(sequenceId, 10)
	return key
}

// GenerateRedisKeyForAllServiceSfdcFieldMapping returns a single global key for the full
// service_sfdc_field_mapping table. Because this table has no partner column, all partners
// share the same data, so one key is sufficient.
func (u RedisKeyGenerator) GenerateRedisKeyForAllServiceSfdcFieldMapping() string {
	env := commoninit.GetConfigString("Environment")
	return constants.SERVICE_NAME_KEY + env + "serviceSfdcFieldMapping:all"
}
