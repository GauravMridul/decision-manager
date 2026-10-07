package utility

import (
	"context"
	"testing"

	"decision-manager/internal/app/types"
)

func TestP0_ArrayFilterAndOverlayBehaviorStable(t *testing.T) {
	updater := newBenchmarkUpdater()
	ctx := context.Background()

	mapping := []types.SFRequestDB{
		{
			URL:       "/services/data/v64.0/sobjects/MultiBureau_AccountList__c",
			Method:    "POST",
			RefID:     "P0_Filter_Overlay",
			ArrayPath: "CIBIL.response.body.consumerCreditData[0].accounts",
			Body: map[string]interface{}{
				"Lead__c":            "((rootLead.id))",
				"Current_Balance__c": "((current.currentBalance))",
			},
			FilterConditions: &types.FilterConditions{
				SkipWhen:   "{{((current.currentBalance)) < 1000}}",
				SelectWhen: "{{((current.currentBalance)) < 2000}}",
			},
		},
	}

	valueJSON := map[string]interface{}{
		"CIBIL": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"consumerCreditData": []interface{}{
						map[string]interface{}{
							"accounts": []interface{}{
								map[string]interface{}{"currentBalance": 999, "enabled": true},
								map[string]interface{}{"currentBalance": 1000, "enabled": true},
								map[string]interface{}{"currentBalance": 1500, "enabled": false},
								map[string]interface{}{"currentBalance": 2500, "enabled": true},
							},
						},
					},
				},
			},
		},
		"rootLead": map[string]interface{}{
			"id": "00QOW00000gv3cC2AQ",
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(ctx, mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 requests after skip/select filters, got %d", len(got))
	}

	first := got[0].Body
	second := got[1].Body

	if first["Lead__c"] != "00QOW00000gv3cC2AQ" || second["Lead__c"] != "00QOW00000gv3cC2AQ" {
		t.Fatalf("expected root lead id to be preserved in both requests, got %v and %v", first["Lead__c"], second["Lead__c"])
	}
	if first["Current_Balance__c"] != 1000 || second["Current_Balance__c"] != 1500 {
		t.Fatalf("unexpected balances: first=%v second=%v", first["Current_Balance__c"], second["Current_Balance__c"])
	}
}

func TestP0_MissingExpressionVariableOmittedWithoutFailure(t *testing.T) {
	updater := newBenchmarkUpdater()
	ctx := context.Background()

	mapping := []types.SFRequestDB{
		{
			URL:       "/services/data/v64.0/sobjects/Test__c",
			Method:    "POST",
			RefID:     "P0_Missing_Var",
			ArrayPath: "CIBIL.response.body.consumerCreditData[0].accounts",
			Body: map[string]interface{}{
				"Stable_Field__c":  "present",
				"Missing_Field__c": "{{((current.missingField))}}",
			},
		},
	}

	valueJSON := map[string]interface{}{
		"CIBIL": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"consumerCreditData": []interface{}{
						map[string]interface{}{
							"accounts": []interface{}{
								map[string]interface{}{"accountType": "05"},
							},
						},
					},
				},
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(ctx, mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 generated request, got %d", len(got))
	}

	if got[0].Body["Stable_Field__c"] != "present" {
		t.Fatalf("expected stable field to be preserved, got %v", got[0].Body["Stable_Field__c"])
	}
	if _, exists := got[0].Body["Missing_Field__c"]; exists {
		t.Fatalf("expected unresolved expression field to be omitted, got %v", got[0].Body["Missing_Field__c"])
	}
}

func TestP0_MixedRegularAndArrayTemplateOrderingStable(t *testing.T) {
	updater := newBenchmarkUpdater()
	ctx := context.Background()

	mapping := []types.SFRequestDB{
		{
			URL:    "/services/data/v64.0/sobjects/Lead",
			Method: "PATCH",
			RefID:  "Regular_Lead_Update",
			Body: map[string]interface{}{
				"Lead__c": "((rootLead.id))",
			},
		},
		{
			URL:       "/services/data/v64.0/sobjects/Account",
			Method:    "POST",
			RefID:     "Array_Account_Create",
			ArrayPath: "CIBIL.response.body.consumerCreditData[0].accounts",
			Body: map[string]interface{}{
				"Account_Type__c": "((current.accountType))",
			},
		},
	}

	valueJSON := map[string]interface{}{
		"rootLead": map[string]interface{}{
			"id": "00QOW00000gv3cC2AQ",
		},
		"CIBIL": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"consumerCreditData": []interface{}{
						map[string]interface{}{
							"accounts": []interface{}{
								map[string]interface{}{"accountType": "01"},
								map[string]interface{}{"accountType": "02"},
							},
						},
					},
				},
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(ctx, mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 total requests (1 regular + 2 array), got %d", len(got))
	}
	if got[0].RefID != "Regular_Lead_Update" {
		t.Fatalf("expected regular template output first, got refID=%s", got[0].RefID)
	}
	if got[1].RefID != "Array_Account_Create_0" || got[2].RefID != "Array_Account_Create_1" {
		t.Fatalf("expected deterministic array refIds, got %s and %s", got[1].RefID, got[2].RefID)
	}
}
