package utility

import (
	"context"
	"encoding/json"
	"testing"

	"decision-manager/internal/app/types"
)

// TestFilterConditions_NonArrayTemplate_SkipWhenIgnored demonstrates that
// filterConditions is NOT evaluated for non-array templates (no arrayPath).
// This is the bug: the request with filterConditions.skipWhen goes through
// unfiltered when there is no arrayPath, and the unresolved <offer__c.Id>
// URL causes a 404 from Salesforce.
func TestFilterConditions_NonArrayTemplate_SkipWhenIgnored(t *testing.T) {
	updater := newBenchmarkUpdater()

	// Simulate the ESA response where offer__c is an empty object
	esaResponseBody := `{
		"data": {
			"offer__c": {},
			"lead": {
				"Id": "00Q9H00000Gp8GtUAJ",
				"No_of_re_decisioning__c": 1
			},
			"contact": {
				"Id": "0039H00000Mnt9yQAB"
			}
		},
		"esaServices": []
	}`

	valueJSON, err := TransformESAResponse(esaResponseBody)
	if err != nil {
		t.Fatalf("failed to transform ESA response: %v", err)
	}

	// This is the exact configuration from the bug report:
	// Non-array template with filterConditions.skipWhen
	mapping := []types.SFRequestDB{
		{
			URL:    "/services/data/v64.0/sobjects/Offer__c/<offer__c.Id>",
			Method: "PATCH",
			RefID:  "PostBureauAmountCRIF_Offer_Parent__c_patch",
			Body: map[string]interface{}{
				"Lead__c":              "<lead.Id>",
				"Decision_Count__c":    "{{lead.No_of_re_decisioning__c+1}}",
				"Customer_Category__c": "CATEGORY C",
			},
			FilterConditions: &types.FilterConditions{
				SkipWhen: "{{((offer__c.Id))!=''}}",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected interpolation error: %v", err)
	}

	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("Result (non-array template with filterConditions): %s", gotJSON)

	// BUG: filterConditions is ignored for non-array templates.
	// The request goes through even though offer__c.Id is empty,
	// and the URL remains unresolved as literal "<offer__c.Id>".
	//
	// Expected behavior: the request should be SKIPPED because offer__c has
	// no Id field. But since filterConditions only applies to array templates,
	// the request is emitted with a broken URL.
	if len(got) == 0 {
		t.Log("PASS: filterConditions was honored (request skipped) - this means the bug is fixed")
	} else {
		t.Logf("BUG CONFIRMED: filterConditions.skipWhen was IGNORED for non-array template")
		t.Logf("  Got %d request(s) instead of 0", len(got))
		// Check if the URL is unresolved
		if len(got) > 0 && got[0].URL == "/services/data/v64.0/sobjects/Offer__c/<offer__c.Id>" {
			t.Logf("  URL is unresolved: %s", got[0].URL)
			t.Logf("  This causes a 404 from Salesforce Composite Graph API")
		}
		t.Fatalf("filterConditions.skipWhen is not supported on non-array (regular) templates; the request was not skipped")
	}
}

// TestFilterConditions_WithArrayPath_SkipWhenWorks demonstrates that
// filterConditions works correctly when an arrayPath is present.
// This shows the expected behavior if the configuration had an arrayPath.
func TestFilterConditions_WithArrayPath_SkipWhenWorks(t *testing.T) {
	updater := newBenchmarkUpdater()

	// Simulate the ESA response where offer__c has records (as an array)
	esaResponseBody := `{
		"data": {
			"offer__c": {
				"records": [
					{"Id": "a0B000001abc", "Name": "Offer 1"},
					{"Id": "", "Name": "Offer 2 no ID"}
				]
			},
			"lead": {
				"Id": "00Q9H00000Gp8GtUAJ",
				"No_of_re_decisioning__c": 1
			},
			"contact": {
				"Id": "0039H00000Mnt9yQAB"
			}
		},
		"esaServices": []
	}`

	valueJSON, err := TransformESAResponse(esaResponseBody)
	if err != nil {
		t.Fatalf("failed to transform ESA response: %v", err)
	}

	// Same template but WITH arrayPath - filterConditions applies per element
	mapping := []types.SFRequestDB{
		{
			URL:       "/services/data/v64.0/sobjects/Offer__c/<current.Id>",
			Method:    "PATCH",
			RefID:     "PostBureauAmountCRIF_Offer_Parent__c_patch_{{index}}",
			ArrayPath: "offer__c.records",
			Body: map[string]interface{}{
				"Lead__c":              "<lead.Id>",
				"Decision_Count__c":    "{{lead.No_of_re_decisioning__c+1}}",
				"Customer_Category__c": "CATEGORY C",
			},
			FilterConditions: &types.FilterConditions{
				// Skip elements where Id IS empty (can't PATCH without an Id)
				SkipWhen: "{{((current.Id))==''}}",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected interpolation error: %v", err)
	}

	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("Result (array template with filterConditions): %s", gotJSON)

	// Should produce exactly 1 request (for the element with Id="a0B000001abc")
	// The second element (Id="") should be skipped by skipWhen
	if len(got) != 1 {
		t.Fatalf("expected 1 request (skipping empty-Id element), got %d", len(got))
	}

	if got[0].URL != "/services/data/v64.0/sobjects/Offer__c/a0B000001abc" {
		t.Fatalf("expected URL with resolved Id, got: %s", got[0].URL)
	}
	t.Log("PASS: filterConditions.skipWhen correctly skipped the element with empty Id")
}

// TestFilterConditions_EmptyObject_SkipLogic verifies expression evaluation
// behavior when referencing a field on an empty object.
func TestFilterConditions_EmptyObject_SkipLogic(t *testing.T) {
	updater := newBenchmarkUpdater()

	// When offer__c is {}, ((offer__c.Id)) should resolve to "" (empty)
	esaResponseBody := `{
		"data": {
			"offer__c": {},
			"lead": {"Id": "LEAD001", "No_of_re_decisioning__c": 1}
		},
		"esaServices": []
	}`

	valueJSON, err := TransformESAResponse(esaResponseBody)
	if err != nil {
		t.Fatalf("failed to transform ESA response: %v", err)
	}

	// Use arrayPath with a dummy array to test expression evaluation
	// This proves that ((offer__c.Id)) evaluates to "" when offer__c is {}
	mapping := []types.SFRequestDB{
		{
			URL:       "/services/data/v64.0/sobjects/TestObject__c",
			Method:    "POST",
			RefID:     "test_{{index}}",
			ArrayPath: "offer__c.records", // This won't resolve - empty object has no records
			Body: map[string]interface{}{
				"Name": "test",
			},
			FilterConditions: &types.FilterConditions{
				SkipWhen: "{{((offer__c.Id))!=''}}",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected interpolation error: %v", err)
	}

	// offer__c is {}, so "offer__c.records" arrayPath won't resolve to an array.
	// The template falls back to a regular template, filterConditions is ignored.
	t.Logf("Result count: %d (offer__c is empty object, arrayPath fallback to regular)", len(got))

	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("Result: %s", gotJSON)
}

// TestConditionalBody_OfferMissingFromESA tests what happens when offer__c
// is completely absent from the ESA response data (not even an empty {}).
func TestConditionalBody_OfferMissingFromESA(t *testing.T) {
	updater := newBenchmarkUpdater()

	// offer__c is NOT present at all in data
	esaResponseBody := `{
		"data": {
			"lead": {
				"Id": "00Q9H00000Gp8GtUAJ",
				"No_of_re_decisioning__c": 1
			},
			"contact": {
				"Id": "0039H00000Mnt9yQAB"
			}
		},
		"esaServices": []
	}`

	valueJSON, err := TransformESAResponse(esaResponseBody)
	if err != nil {
		t.Fatalf("failed to transform ESA response: %v", err)
	}

	// User's exact config with offer__c=={} check
	mapping := []types.SFRequestDB{
		{
			URL:    "/services/data/v64.0/sobjects/Offer__c/<offer__c.Id>",
			Method: "PATCH",
			RefID:  "PostBureauAmountCRIF_Offer_Parent__c_patch",
			Body: map[string]interface{}{
				"Lead__c":              "{{offer__c=={}?'':lead.Id}}",
				"Decision_Count__c":    "{{offer__c=={}?'':lead.No_of_re_decisioning__c+1}}",
				"Customer_Category__c": "{{offer__c=={}?'':'CATEGORY C'}}",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected interpolation error: %v", err)
	}

	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("Result when offer__c is MISSING from ESA response: %s", gotJSON)

	if len(got) == 0 {
		t.Log("Result: No request generated")
	} else {
		t.Logf("Result: %d request(s), body has %d field(s)", len(got), len(got[0].Body))
		for k, v := range got[0].Body {
			t.Logf("  Body[%s] = %v (%T)", k, v, v)
		}
		if len(got[0].Body) == 0 {
			t.Log("PASS: Body is empty — will be filtered by buildUpsertCompositeRequest")
		} else {
			t.Log("ISSUE: Body has fields — offer__c=={} check did NOT catch missing key")
		}
	}
}

// TestConditionalBody_ExactUserConfig_OfferEmptyObject tests EXACTLY the
// user's proposed configuration against the real ESA response where offer__c is {}.
//
// Config under test:
//
//	{
//	  "url": "/services/data/v64.0/sobjects/Offer__c/<offer__c.Id>",
//	  "body": {
//	    "Lead__c": "{{offer__c=={}?'':lead.Id}}",
//	    "Decision_Count__c": "{{offer__c=={}?'':lead.No_of_re_decisioning__c+1}}",
//	    "Customer_Category__c": "{{offer__c=={}?'':'CATEGORY C'}}"
//	  },
//	  "merge": "false",
//	  "method": "PATCH",
//	  "referenceId": "PostBureauAmountCRIF_Offer_Parent__c_patch"
//	}
func TestConditionalBody_ExactUserConfig_OfferEmptyObject(t *testing.T) {
	updater := newBenchmarkUpdater()

	// Exact ESA response structure (trimmed to relevant parts)
	esaResponseBody := `{
		"data": {
			"offer__c": {},
			"lead": {
				"Id": "00Q9H00000Gp8GtUAJ",
				"No_of_re_decisioning__c": 1,
				"Loan_Rate__c": 20,
				"Loan_Tenor_in_Month__c": 24
			},
			"contact": {
				"Id": "0039H00000Mnt9yQAB"
			}
		},
		"esaServices": []
	}`

	valueJSON, err := TransformESAResponse(esaResponseBody)
	if err != nil {
		t.Fatalf("failed to transform ESA response: %v", err)
	}

	// EXACT user configuration — testing offer__c=={} syntax
	mapping := []types.SFRequestDB{
		{
			URL:    "/services/data/v64.0/sobjects/Offer__c/<offer__c.Id>",
			Method: "PATCH",
			RefID:  "PostBureauAmountCRIF_Offer_Parent__c_patch",
			Body: map[string]interface{}{
				"Lead__c":              "{{offer__c=={}?'':lead.Id}}",
				"Decision_Count__c":    "{{offer__c=={}?'':lead.No_of_re_decisioning__c+1}}",
				"Customer_Category__c": "{{offer__c=={}?'':'CATEGORY C'}}",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected interpolation error: %v", err)
	}

	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("Result with EXACT user config (offer__c=={}): %s", gotJSON)

	// Analyze what happened
	if len(got) == 0 {
		t.Log("Result: No request generated")
	} else {
		t.Logf("Result: %d request(s), body has %d field(s)", len(got), len(got[0].Body))
		for k, v := range got[0].Body {
			t.Logf("  Body[%s] = %v (%T)", k, v, v)
		}
	}

	// Now check: does body become empty (which means SF won't receive it)?
	if len(got) == 1 && len(got[0].Body) == 0 {
		t.Log("PASS: Body is empty — buildUpsertCompositeRequest will filter this out")
	} else if len(got) == 1 && len(got[0].Body) > 0 {
		t.Log("FAIL: Body still has fields — the offer__c=={} syntax did NOT work as expected")
		t.Log("      govaluate cannot compare a map to {} literal.")
		t.Log("      Use ((offer__c.Id))=='' instead.")
		t.FailNow()
	}
}
