package repository

import (
	"strings"
	"testing"
)

func TestPartnerServiceMappingSpecificityOrderClause_HasAllSpecificityChecks(t *testing.T) {
	requiredColumns := []string{
		"partner_name",
		"program_type",
		"business_type",
		"sourcing_program",
		"loan_category",
		"customer_type",
		"product_line",
		"sales_channel_partner_name",
		"sourcing_channel",
		"name_of_consolidator",
	}

	for _, col := range requiredColumns {
		expected := "CASE WHEN " + col + " = '' THEN 1 ELSE 0 END"
		if !strings.Contains(partnerServiceMappingSpecificityOrderClause, expected) {
			t.Fatalf("missing specificity clause for column %q", col)
		}
	}
}

func TestPartnerServiceMappingSpecificityOrderClause_DoesNotUseNullsLast(t *testing.T) {
	if strings.Contains(strings.ToUpper(partnerServiceMappingSpecificityOrderClause), "NULLS LAST") {
		t.Fatalf("order clause must not use NULLS LAST for empty-string precedence")
	}
}
