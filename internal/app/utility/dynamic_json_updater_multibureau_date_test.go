package utility

import (
	"regexp"
	"testing"
)

// This test documents the behavior of the Multibureau_Date__c interpolation:
//
//	{{multibureau_data__c.CreatedDate!=""?multibureau_data__c.CreatedDate:formatDate(now(),'YYYY-MM-DDTHH:mm:ss.SSSZ')}}
//
// Expected behavior:
//   - CreatedDate present + non-empty  -> pass the same value through unchanged
//   - multibureau_data__c absent       -> use formatDate(now(), ...)
//   - CreatedDate key missing          -> use formatDate(now(), ...)
//   - CreatedDate empty string         -> use formatDate(now(), ...)
//   - CreatedDate explicit null        -> use formatDate(now(), ...)
const multibureauDateExpr = `{{multibureau_data__c.CreatedDate!=""?multibureau_data__c.CreatedDate:formatDate(now(),'YYYY-MM-DDTHH:mm:ss.SSSZ')}}`

// Matches formatDate(now(),'YYYY-MM-DDTHH:mm:ss.SSSZ') output, e.g.
// 2026-07-17T12:34:56.789Z  or  2026-07-17T12:34:56.789+0000
var nowBranchPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(Z|[+-]\d{4})$`)

func evalMultibureauDate(t *testing.T, valueJSON map[string]interface{}) string {
	t.Helper()
	got, err := evaluateExpression(multibureauDateExpr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error evaluating expression: %v", err)
	}
	s, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T (%v)", got, got)
	}
	return s
}

// Scenario 1: CreatedDate present with a valid, non-empty value -> same value passed through.
func TestMultibureauDate_CreatedDatePresent_PassesThrough(t *testing.T) {
	const created = "2026-05-21T08:14:38.000+0000"
	valueJSON := map[string]interface{}{
		"multibureau_data__c": map[string]interface{}{
			"Id":          "a1k9H00000dQqMIQA0",
			"CreatedDate": created,
		},
	}

	got := evalMultibureauDate(t, valueJSON)
	if got != created {
		t.Fatalf("expected CreatedDate to pass through as %q, got %q", created, got)
	}
}

// Scenario 2: multibureau_data__c absent entirely from data -> now().
func TestMultibureauDate_MultibureauAbsent_UsesNow(t *testing.T) {
	valueJSON := map[string]interface{}{
		"contact": map[string]interface{}{
			"Id": "0039H00000LvM1KQAV",
		},
	}

	got := evalMultibureauDate(t, valueJSON)
	if !nowBranchPattern.MatchString(got) {
		t.Fatalf("expected now() formatted date, got %q", got)
	}
}

// Scenario 2b: multibureau_data__c present but empty object ({}) -> now().
// (This is how ESA renders a child sObject with no record.)
func TestMultibureauDate_MultibureauEmptyObject_UsesNow(t *testing.T) {
	valueJSON := map[string]interface{}{
		"multibureau_data__c": map[string]interface{}{},
	}

	got := evalMultibureauDate(t, valueJSON)
	if !nowBranchPattern.MatchString(got) {
		t.Fatalf("expected now() formatted date, got %q", got)
	}
}

// Scenario 3: CreatedDate key missing (object present, other fields only) -> now().
func TestMultibureauDate_CreatedDateMissing_UsesNow(t *testing.T) {
	valueJSON := map[string]interface{}{
		"multibureau_data__c": map[string]interface{}{
			"Id": "a1k9H00000dQqMIQA0",
		},
	}

	got := evalMultibureauDate(t, valueJSON)
	if !nowBranchPattern.MatchString(got) {
		t.Fatalf("expected now() formatted date, got %q", got)
	}
}

// Scenario 4: CreatedDate is an empty string -> now().
func TestMultibureauDate_CreatedDateEmpty_UsesNow(t *testing.T) {
	valueJSON := map[string]interface{}{
		"multibureau_data__c": map[string]interface{}{
			"Id":          "a1k9H00000dQqMIQA0",
			"CreatedDate": "",
		},
	}

	got := evalMultibureauDate(t, valueJSON)
	if !nowBranchPattern.MatchString(got) {
		t.Fatalf("expected now() formatted date, got %q", got)
	}
}

// Scenario 5: CreatedDate is explicit JSON null (Go nil) -> uses now().
//
// Note: govaluate's `!=` uses reflect.DeepEqual, so the condition
// `nil != ""` is actually TRUE. It still resolves to now() because govaluate
// implements the ternary as two chained operators: `(a ? b) : c`.
//   - `true ? nil`  -> nil   (if-stage returns the true value, which is nil here)
//   - `nil : now()` -> now() (else-stage returns the else value whenever the
//     if-result is nil)
// So a null CreatedDate deterministically falls through to now().
func TestMultibureauDate_CreatedDateNull_UsesNow(t *testing.T) {
	valueJSON := map[string]interface{}{
		"multibureau_data__c": map[string]interface{}{
			"Id":          "a1k9H00000dQqMIQA0",
			"CreatedDate": nil,
		},
	}

	got := evalMultibureauDate(t, valueJSON)
	if !nowBranchPattern.MatchString(got) {
		t.Fatalf("expected now() formatted date, got %q", got)
	}
}
