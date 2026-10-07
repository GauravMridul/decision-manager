package utility

import (
	"context"
	"testing"

	"decision-manager/internal/app/types"
)

func newLookupTestUpdater() *DynamicJsonUpdater {
	updater := &DynamicJsonUpdater{
		log: testLogger{},
	}
	updater.setArrayConfig(&ArrayProcessingConfig{
		MemoryPoolThreshold:     20,
		EnableMemoryPool:        false,
		FallbackToSingleElement: false,
		SkipInvalidArrayPaths:   true,
	})
	return updater
}

func TestInterpolateValuesWithArrayExpansion_LookupJoinByKey(t *testing.T) {
	updater := newLookupTestUpdater()

	valueJSON := map[string]interface{}{
		"KarzaGSTNonOTP": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"results": []interface{}{
						map[string]interface{}{"gstid": "  aa11 "},
						map[string]interface{}{"gstid": "bb22"},
					},
				},
			},
		},
		"OtherService": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"results": []interface{}{
						map[string]interface{}{
							"gstin":                            "AA11",
							"Email_for_principal_place":        "first@example.com",
							"Nature_of_business_carried":       "Manufacturing",
							"irrelevant_field_for_lookup_test": "x",
						},
						map[string]interface{}{
							"gstin":                      "aa11", // duplicate should be ignored (first wins)
							"Email_for_principal_place":  "second@example.com",
							"Nature_of_business_carried": "Services",
						},
					},
				},
			},
		},
	}

	mapping := []types.SFRequestDB{
		{
			URL:       "/services/data/v64.0/sobjects/Contact_Tax_Detail__c",
			Method:    "POST",
			RefID:     "KarzaGSTNonOTP_Contact_Tax_Detail__c_Post",
			ArrayPath: "KarzaGSTNonOTP.response.body.results",
			Lookup: &types.LookupConfig{
				As:             "gstJoin",
				FromArrayPath:  "OtherService.response.body.results",
				CurrentKeyPath: "gstid",
				LookupKeyPath:  "gstin",
			},
			Body: map[string]interface{}{
				"GST_number__c": "((current.gstid))",
				"Email_for_principal_place_of_business__c":     "((gstJoin.Email_for_principal_place))",
				"Nature_of_business_carried_out_at_princi__c":  "((gstJoin.Nature_of_business_carried))",
				"Always_present_field_for_sanity_check__c":     "static-value",
				"Missing_lookup_field_should_be_omitted_test":  "((gstJoin.nonExistent))",
				"Missing_current_field_should_be_omitted_test": "((current.unknownField))",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 requests from array expansion, got %d", len(got))
	}

	first := got[0].Body
	if first["GST_number__c"] != "  aa11 " {
		t.Fatalf("expected GST_number__c from current element, got %#v", first["GST_number__c"])
	}
	if first["Email_for_principal_place_of_business__c"] != "first@example.com" {
		t.Fatalf("expected first duplicate lookup match to win, got %#v", first["Email_for_principal_place_of_business__c"])
	}
	if first["Nature_of_business_carried_out_at_princi__c"] != "Manufacturing" {
		t.Fatalf("expected joined nature_of_business from lookup, got %#v", first["Nature_of_business_carried_out_at_princi__c"])
	}
	if _, exists := first["Missing_lookup_field_should_be_omitted_test"]; exists {
		t.Fatalf("expected unresolved lookup field to be omitted")
	}
	if _, exists := first["Missing_current_field_should_be_omitted_test"]; exists {
		t.Fatalf("expected unresolved current field to be omitted")
	}

	second := got[1].Body
	if second["GST_number__c"] != "bb22" {
		t.Fatalf("expected GST_number__c for second element, got %#v", second["GST_number__c"])
	}
	if _, exists := second["Email_for_principal_place_of_business__c"]; exists {
		t.Fatalf("expected joined email to be omitted when no lookup match exists")
	}
	if _, exists := second["Nature_of_business_carried_out_at_princi__c"]; exists {
		t.Fatalf("expected joined nature to be omitted when no lookup match exists")
	}
	if second["Always_present_field_for_sanity_check__c"] != "static-value" {
		t.Fatalf("expected static field to remain populated, got %#v", second["Always_present_field_for_sanity_check__c"])
	}
}

func TestInterpolateValuesWithArrayExpansion_InvalidLookupConfigFallsBackGracefully(t *testing.T) {
	updater := newLookupTestUpdater()

	valueJSON := map[string]interface{}{
		"KarzaGSTNonOTP": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"results": []interface{}{
						map[string]interface{}{"gstid": "AA11"},
					},
				},
			},
		},
	}

	mapping := []types.SFRequestDB{
		{
			URL:       "/services/data/v64.0/sobjects/Contact_Tax_Detail__c",
			Method:    "POST",
			RefID:     "KarzaGSTNonOTP_Contact_Tax_Detail__c_Post",
			ArrayPath: "KarzaGSTNonOTP.response.body.results",
			Lookup: &types.LookupConfig{
				As:             "", // invalid alias should disable lookup
				FromArrayPath:  "OtherService.response.body.results",
				CurrentKeyPath: "gstid",
				LookupKeyPath:  "gstin",
			},
			Body: map[string]interface{}{
				"GST_number__c": "((current.gstid))",
				"Email_for_principal_place_of_business__c": "((gstJoin.Email_for_principal_place))",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected single request, got %d", len(got))
	}
	if got[0].Body["GST_number__c"] != "AA11" {
		t.Fatalf("expected base current mapping to continue working, got %#v", got[0].Body["GST_number__c"])
	}
	if _, exists := got[0].Body["Email_for_principal_place_of_business__c"]; exists {
		t.Fatalf("expected lookup field to be omitted when lookup config is invalid")
	}
}

func TestInterpolateValuesWithArrayExpansion_LookupJoinWithNestedPaths(t *testing.T) {
	updater := newLookupTestUpdater()

	valueJSON := map[string]interface{}{
		"KarzaGSTNonOTP": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"results": []interface{}{
						map[string]interface{}{
							"data": map[string]interface{}{
								"gstId": "27abc",
							},
						},
					},
				},
			},
		},
		"OtherService": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"results": []interface{}{
						map[string]interface{}{
							"data": map[string]interface{}{
								"gstId": "27ABC",
							},
							"Email_for_principal_place": "nested@example.com",
						},
					},
				},
			},
		},
	}

	mapping := []types.SFRequestDB{
		{
			URL:       "/services/data/v64.0/sobjects/Contact_Tax_Detail__c",
			Method:    "POST",
			RefID:     "KarzaGSTNonOTP_Contact_Tax_Detail__c_Post",
			ArrayPath: "KarzaGSTNonOTP.response.body.results",
			Lookup: &types.LookupConfig{
				As:             "gstJoin",
				FromArrayPath:  "OtherService.response.body.results",
				CurrentKeyPath: "current.data.gstId",
				LookupKeyPath:  "data.gstId",
			},
			Body: map[string]interface{}{
				"GST_number__c": "((current.data.gstId))",
				"Email_for_principal_place_of_business__c": "((gstJoin.Email_for_principal_place))",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 request from array expansion, got %d", len(got))
	}

	body := got[0].Body
	if body["GST_number__c"] != "27abc" {
		t.Fatalf("expected nested current field value, got %#v", body["GST_number__c"])
	}
	if body["Email_for_principal_place_of_business__c"] != "nested@example.com" {
		t.Fatalf("expected nested lookup join match, got %#v", body["Email_for_principal_place_of_business__c"])
	}
}
