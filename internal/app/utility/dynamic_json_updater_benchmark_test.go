package utility

import (
	"context"
	"strconv"
	"testing"

	"decision-manager/internal/app/types"
)

func BenchmarkArrayToString_ExpandedPrimitiveArgs(b *testing.B) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		b.Fatal("arrayToString function not found")
	}

	args := []interface{}{
		"LOAN AMOUNT NORMS NOT MET",
		"IXSIGHT NEGATIVE LIST MATCH FOUND",
		"INCOME NORMS NOT MET",
		"ELIGIBILITY NORMS NOT MET",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := fn(args...)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

func BenchmarkArrayToString_ObjectFieldExtraction(b *testing.B) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		b.Fatal("arrayToString function not found")
	}

	objArr := []interface{}{
		map[string]interface{}{"reason": "A SCORE NORMS NOT MET - STPL V6"},
		map[string]interface{}{"reason": "BANKING AA NORMS NOT MET"},
		map[string]interface{}{"reason": "INCOME NORMS NOT MET"},
		map[string]interface{}{"reason": "ELIGIBILITY NORMS NOT MET"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := fn(objArr, "reason")
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

func newBenchmarkUpdater() *DynamicJsonUpdater {
	updater := &DynamicJsonUpdater{
		log:                   testLogger{},
		MaxParallelGoroutines: 16,
	}
	updater.setArrayConfig(&ArrayProcessingConfig{
		MemoryPoolThreshold:      20,
		EnableMemoryPool:         true,
		FallbackToSingleElement:  false,
		SkipInvalidArrayPaths:    true,
		CollectUnresolvedDetails: false,
	})
	return updater
}

func buildLargeArrayBenchmarkInput(elementCount int) ([]types.SFRequestDB, map[string]interface{}) {
	accounts := make([]interface{}, 0, elementCount)
	for i := 0; i < elementCount; i++ {
		accounts = append(accounts, map[string]interface{}{
			"accountType":      "05",
			"currentBalance":   1000 + i,
			"highCreditAmount": 2000 + i,
			"dateReported":     "2026-03-26",
			"ownership":        1,
			"tenantId":         "t-" + strconv.Itoa(i%20),
			"enabled":          i%2 == 0,
		})
	}

	valueJSON := map[string]interface{}{
		"CIBIL": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"consumerCreditData": []interface{}{
						map[string]interface{}{
							"accounts": accounts,
						},
					},
				},
			},
		},
		"rootLead": map[string]interface{}{
			"id": "00QOW00000gv3cC2AQ",
		},
	}

	mapping := []types.SFRequestDB{
		{
			URL:       "/services/data/v64.0/sobjects/MultiBureau_AccountList__c",
			Method:    "POST",
			RefID:     "CIBIL_AccountList_Post",
			ArrayPath: "CIBIL.response.body.consumerCreditData[0].accounts",
			Body: map[string]interface{}{
				"Account_Type__c":                     "((current.accountType))",
				"Current_Balance__c":                  "((current.currentBalance))",
				"High_Credit_or_Sanctioned_Amount__c": "((current.highCreditAmount))",
				"Date_Reported_And_Certified__c":      "((current.dateReported))",
				"Ownership_Indicator__c":              "((current.ownership))",
				"Lead__c":                             "((rootLead.id))",
				"Is_Enabled__c":                       "{{((current.enabled)) == true}}",
				"Tenant_Mod__c":                       "{{mod((current.currentBalance), 7)}}",
				"Tenant_Id__c":                        "((current.tenantId))",
			},
			FilterConditions: &types.FilterConditions{
				SkipWhen: "{{((current.currentBalance)) < 1000}}",
			},
		},
	}

	return mapping, valueJSON
}

func BenchmarkInterpolateValuesWithArrayExpansion_LargeArray(b *testing.B) {
	updater := newBenchmarkUpdater()
	mapping, valueJSON := buildLargeArrayBenchmarkInput(630)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		got, err := updater.InterpolateValuesWithArrayExpansion(ctx, mapping, valueJSON)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
		if len(got) == 0 {
			b.Fatalf("expected expanded requests, got none")
		}
	}
}
