package utility

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// Tests for the selectByFieldSorted custom function. All cases go through the
// real expression engine (evaluateExpression) so govaluate parsing, arg
// spreading, and param flattening are exercised end-to-end.
//
// selectByFieldSorted(array, matchFieldPath, matchValue, sortFieldPath, order, extractFieldPath)

// evalSelect builds the expression for the given wrapped-array reference and
// params, evaluates it against payload, and returns the raw result.
func evalSelect(t *testing.T, payload map[string]interface{}, arrayRef, matchField, matchVal, sortField, order, extractField string) interface{} {
	t.Helper()
	expr := fmt.Sprintf("{{selectByFieldSorted(((%s)),'%s','%s','%s','%s','%s')}}",
		arrayRef, matchField, matchVal, sortField, order, extractField)
	got, err := evaluateExpression(expr, payload, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error for expr %s: %v", expr, err)
	}
	return got
}

func mustString(t *testing.T, v interface{}) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string result, got %T (%v)", v, v)
	}
	return s
}

// crifRecord builds a record shaped like multibureau_consolidate_data__c.records[i].
func crifRecord(id, bureau string, mbDate interface{}, createdDate string, pdf interface{}) map[string]interface{} {
	return map[string]interface{}{
		"Id":                  id,
		"Multibureau_Date__c": mbDate,
		"Multibureau__r": map[string]interface{}{
			"Bureau__c":            bureau,
			"CreatedDate":          createdDate,
			"Credit_Bureau_pdf__c": pdf,
		},
	}
}

// ---------------------------------------------------------------------------
// Deterministic scenario tests
// ---------------------------------------------------------------------------

// Latest CRIF by Multibureau_Date__c, extracting nested pdf. Also proves that
// a different bureau with a later date is excluded by the filter.
func TestSelect_LatestCrif_NestedFilterSortExtract(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", "2026-01-01T00:00:00.000+0000", "2026-01-01T00:00:00.000+0000", "pdf-old"),
			crifRecord("r2", "CRIF", "2026-07-23T07:53:50.000+0000", "2026-07-23T07:53:50.000+0000", "pdf-latest"),
			crifRecord("r3", "CIBIL", "2026-12-31T00:00:00.000+0000", "2026-12-31T00:00:00.000+0000", "pdf-cibil-newer"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-latest" {
		t.Fatalf("expected pdf-latest, got %q", s)
	}
}

// Sort on a NESTED date field (Multibureau__r.CreatedDate) rather than the
// top-level date.
func TestSelect_SortByNestedCreatedDate(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", nil, "2025-03-03T00:00:00.000+0000", "pdf-a"),
			crifRecord("r2", "CRIF", nil, "2026-09-09T00:00:00.000+0000", "pdf-b"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau__r.CreatedDate", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-b" {
		t.Fatalf("expected pdf-b (later CreatedDate), got %q", s)
	}
}

// ascending order returns earliest.
func TestSelect_AscendingReturnsEarliest(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", "2026-01-01T00:00:00.000+0000", "", "pdf-earliest"),
			crifRecord("r2", "CRIF", "2026-07-23T07:53:50.000+0000", "", "pdf-later"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "asc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-earliest" {
		t.Fatalf("expected pdf-earliest, got %q", s)
	}
}

// "latest NOT-NULL date": records with null/empty Multibureau_Date__c are
// dropped; winner is the latest among the non-null ones.
func TestSelect_DropsNullAndEmptySortKeys(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", nil, "", "pdf-null-date"),
			crifRecord("r2", "CRIF", "", "", "pdf-empty-date"),
			crifRecord("r3", "CRIF", "2026-02-02T00:00:00.000+0000", "", "pdf-valid-old"),
			crifRecord("r4", "CRIF", "2026-06-06T00:00:00.000+0000", "", "pdf-valid-latest"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-valid-latest" {
		t.Fatalf("expected pdf-valid-latest, got %q", s)
	}
}

// All CRIF dates null -> nil result (caller falls back to uploadToS3).
func TestSelect_AllSortKeysNull_ReturnsNil(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", nil, "", "pdf-a"),
			crifRecord("r2", "CRIF", nil, "", "pdf-b"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if got != nil {
		t.Fatalf("expected nil when all sort keys null, got %T (%v)", got, got)
	}
}

// Winner found but its extract field is null -> returns nil (ternary -> fallback).
func TestSelect_WinnerExtractFieldNull_ReturnsNil(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", "2026-01-01T00:00:00.000+0000", "", "pdf-old"),
			crifRecord("r2", "CRIF", "2026-09-09T00:00:00.000+0000", "", nil), // latest but pdf null
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if got != nil {
		t.Fatalf("expected nil (winner pdf null), got %T (%v)", got, got)
	}
}

// No element matches the filter -> nil.
func TestSelect_NoFilterMatch_ReturnsNil(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CIBIL", "2026-01-01T00:00:00.000+0000", "", "pdf-a"),
			crifRecord("r2", "EXPERIAN", "2026-09-09T00:00:00.000+0000", "", "pdf-b"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if got != nil {
		t.Fatalf("expected nil (no CRIF match), got %T (%v)", got, got)
	}
}

// Filter is case-insensitive ("crif" matches "CRIF").
func TestSelect_FilterCaseInsensitive(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", "2026-05-05T00:00:00.000+0000", "", "pdf-x"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "crif", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-x" {
		t.Fatalf("expected pdf-x, got %q", s)
	}
}

// Single object (records NOT an array) is handled as a one-element set.
func TestSelect_SingleObject_NotArray(t *testing.T) {
	payload := map[string]interface{}{
		"records": crifRecord("r1", "CRIF", "2026-05-05T00:00:00.000+0000", "", "pdf-single"),
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-single" {
		t.Fatalf("expected pdf-single, got %q", s)
	}
}

// Single object that does NOT match the filter -> nil.
func TestSelect_SingleObject_NoMatch(t *testing.T) {
	payload := map[string]interface{}{
		"records": crifRecord("r1", "CIBIL", "2026-05-05T00:00:00.000+0000", "", "pdf-single"),
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if got != nil {
		t.Fatalf("expected nil for non-matching single object, got %v", got)
	}
}

// Empty array -> nil.
func TestSelect_EmptyArray_ReturnsNil(t *testing.T) {
	payload := map[string]interface{}{"records": []interface{}{}}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if got != nil {
		t.Fatalf("expected nil for empty array, got %v", got)
	}
}

// Missing array key entirely -> nil (no error).
func TestSelect_MissingArray_ReturnsNil(t *testing.T) {
	payload := map[string]interface{}{"something_else": 1}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if got != nil {
		t.Fatalf("expected nil for missing array, got %v", got)
	}
}

// No filter (empty matchValue) selects across all elements.
func TestSelect_NoFilter_SelectsAcrossAll(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", "2026-01-01T00:00:00.000+0000", "", "pdf-crif"),
			crifRecord("r2", "CIBIL", "2026-12-31T00:00:00.000+0000", "", "pdf-cibil-latest"),
		},
	}
	got := evalSelect(t, payload, "records", "", "", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-cibil-latest" {
		t.Fatalf("expected pdf-cibil-latest (no filter), got %q", s)
	}
}

// Numeric sort key: pick the max/min score. Also uses a ROOT-level sort field.
func TestSelect_NumericSort_RootField(t *testing.T) {
	mk := func(score interface{}, name string) map[string]interface{} {
		return map[string]interface{}{"Score__c": score, "Name__c": name}
	}
	payload := map[string]interface{}{
		"scores": []interface{}{
			mk("00781", "a"),
			mk(920, "b"), // numeric json value
			mk("150", "c"),
		},
	}
	// desc -> highest numeric (920)
	got := evalSelect(t, payload, "scores", "", "", "Score__c", "desc", "Name__c")
	if s := mustString(t, got); s != "b" {
		t.Fatalf("desc: expected b (920), got %q", s)
	}
	// asc -> lowest numeric (150)
	got2 := evalSelect(t, payload, "scores", "", "", "Score__c", "asc", "Name__c")
	if s := mustString(t, got2); s != "c" {
		t.Fatalf("asc: expected c (150), got %q", s)
	}
}

// String (lexical) sort when keys are neither numeric nor dates.
func TestSelect_StringSort(t *testing.T) {
	mk := func(tag, name string) map[string]interface{} {
		return map[string]interface{}{"Tag__c": tag, "Name__c": name}
	}
	payload := map[string]interface{}{
		"items": []interface{}{
			mk("alpha", "a"), mk("zeta", "z"), mk("mid", "m"),
		},
	}
	got := evalSelect(t, payload, "items", "", "", "Tag__c", "desc", "Name__c")
	if s := mustString(t, got); s != "z" {
		t.Fatalf("expected z (lexical max 'zeta'), got %q", s)
	}
}

// Extract whole element when extractFieldPath is empty.
func TestSelect_ExtractWholeElement(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", "2026-05-05T00:00:00.000+0000", "", "pdf-x"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "")
	m, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected whole element map, got %T", got)
	}
	if m["Id"] != "r1" {
		t.Fatalf("expected element r1, got %v", m["Id"])
	}
}

// Deep-nested extract path (a.b.c.d).
func TestSelect_DeepNestedExtract(t *testing.T) {
	mk := func(d string, when string) map[string]interface{} {
		return map[string]interface{}{
			"when": when,
			"a":    map[string]interface{}{"b": map[string]interface{}{"c": map[string]interface{}{"d": d}}},
		}
	}
	payload := map[string]interface{}{
		"rows": []interface{}{
			mk("deep-old", "2025-01-01"),
			mk("deep-new", "2026-01-01"),
		},
	}
	got := evalSelect(t, payload, "rows", "", "", "when", "desc", "a.b.c.d")
	if s := mustString(t, got); s != "deep-new" {
		t.Fatalf("expected deep-new, got %q", s)
	}
}

// Tie on sort key -> first in input order wins (stable).
func TestSelect_TieKeepsFirst(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", "2026-05-05T00:00:00.000+0000", "", "pdf-first"),
			crifRecord("r2", "CRIF", "2026-05-05T00:00:00.000+0000", "", "pdf-second"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-first" {
		t.Fatalf("expected pdf-first on tie, got %q", s)
	}
}

// Mixed date formats across records still order chronologically.
func TestSelect_MixedDateFormats(t *testing.T) {
	payload := map[string]interface{}{
		"records": []interface{}{
			crifRecord("r1", "CRIF", "2025-01-01", "", "pdf-old"),
			crifRecord("r2", "CRIF", "2026-09-09T07:53:50.000+0000", "", "pdf-new"),
		},
	}
	got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	if s := mustString(t, got); s != "pdf-new" {
		t.Fatalf("expected pdf-new, got %q", s)
	}
}

// End-to-end: the real field template shape (selectByFieldSorted feeding a
// ternary). Present -> pass through; null/none -> fall to the else branch.
func TestSelect_EndToEndTernary(t *testing.T) {
	tmpl := `{{selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.Credit_Bureau_pdf__c')!=""?selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.Credit_Bureau_pdf__c'):formatDate(now(),'YYYY-MM-DDTHH:mm:ss.SSSZ')}}`

	// Case 1: latest CRIF has a pdf -> passthrough.
	p1 := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CRIF", "2026-01-01T00:00:00.000+0000", "", "pdf-old"),
				crifRecord("r2", "CRIF", "2026-09-09T00:00:00.000+0000", "", "pdf-latest"),
			},
		},
	}
	got1, err := evaluateExpression(tmpl, p1, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("case1 error: %v", err)
	}
	if got1 != "pdf-latest" {
		t.Fatalf("case1 expected pdf-latest, got %v", got1)
	}

	// Case 2: latest CRIF pdf null -> falls to now().
	p2 := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CRIF", "2026-09-09T00:00:00.000+0000", "", nil),
			},
		},
	}
	got2, err := evaluateExpression(tmpl, p2, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("case2 error: %v", err)
	}
	if !nowBranchPattern.MatchString(mustString(t, got2)) {
		t.Fatalf("case2 expected now() output, got %v", got2)
	}

	// Case 3: no CRIF at all -> falls to now().
	p3 := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CIBIL", "2026-09-09T00:00:00.000+0000", "", "pdf-cibil"),
			},
		},
	}
	got3, err := evaluateExpression(tmpl, p3, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("case3 error: %v", err)
	}
	if !nowBranchPattern.MatchString(mustString(t, got3)) {
		t.Fatalf("case3 expected now() output, got %v", got3)
	}
}

// ---------------------------------------------------------------------------
// Randomized regression: >5000 cases with an independently-computed oracle.
// ---------------------------------------------------------------------------

func TestSelect_Regression_Randomized(t *testing.T) {
	const iterations = 6000
	r := rand.New(rand.NewSource(0x5E1EC7))

	baseTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	bureaus := []string{"CRIF", "CIBIL", "EXPERIAN", "EQUIFAX"}

	var withMatch, nullWinner, noMatch, singleObj int

	for it := 0; it < iterations; it++ {
		n := r.Intn(6) // 0..5 records
		var records []interface{}

		// Oracle state: track the latest-dated CRIF record with a non-null date.
		type oracle struct {
			ord int // input order index
			t   time.Time
			pdf interface{}
			set bool
		}
		var best oracle

		for i := 0; i < n; i++ {
			bureau := bureaus[r.Intn(len(bureaus))]

			// date: 60% valid, 20% null, 20% empty string
			var mbDate interface{}
			var haveDate bool
			var dt time.Time
			switch r.Intn(5) {
			case 0:
				mbDate = nil
			case 1:
				mbDate = ""
			default:
				dt = baseTime.Add(time.Duration(r.Int63n(int64(2000 * 24 * time.Hour))))
				mbDate = dt.Format("2006-01-02T15:04:05.000-0700")
				haveDate = true
			}

			// pdf: 70% a value, 30% null
			var pdf interface{}
			if r.Intn(10) < 7 {
				pdf = fmt.Sprintf("pdf-%d-%d", it, i)
			} else {
				pdf = nil
			}

			records = append(records, crifRecord(fmt.Sprintf("r%d", i), bureau, mbDate, "", pdf))

			if bureau == "CRIF" && haveDate {
				// desc winner: strictly-later date replaces; tie keeps earlier order (already set)
				if !best.set || dt.After(best.t) {
					best = oracle{ord: i, t: dt, pdf: pdf, set: true}
				}
			}
		}

		// Randomly present as a single object when exactly one record.
		var payload map[string]interface{}
		if n == 1 && r.Intn(2) == 0 {
			payload = map[string]interface{}{"records": records[0]}
			singleObj++
		} else {
			payload = map[string]interface{}{"records": records}
		}

		got := evalSelect(t, payload, "records", "Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")

		if !best.set {
			noMatch++
			if got != nil {
				t.Fatalf("iter=%d expected nil (no CRIF w/ non-null date), got %T (%v)\nrecords=%v", it, got, got, records)
			}
			continue
		}
		// best.pdf is the expected extracted value (may be nil if that record's pdf is null)
		if best.pdf == nil {
			nullWinner++
			if got != nil {
				t.Fatalf("iter=%d expected nil (winner pdf null), got %v\nrecords=%v", it, got, records)
			}
			continue
		}
		withMatch++
		if got != best.pdf {
			t.Fatalf("iter=%d expected %v, got %v\nrecords=%v", it, best.pdf, got, records)
		}
	}

	if withMatch == 0 || nullWinner == 0 || noMatch == 0 || singleObj == 0 {
		t.Fatalf("insufficient scenario coverage: withMatch=%d nullWinner=%d noMatch=%d singleObj=%d",
			withMatch, nullWinner, noMatch, singleObj)
	}
	t.Logf("regression ok: withMatch=%d nullWinner=%d noMatch=%d singleObj=%d", withMatch, nullWinner, noMatch, singleObj)
}

// Randomized numeric-sort regression with an oracle (argmax/argmin by number).
func TestSelect_Regression_NumericArgmaxArgmin(t *testing.T) {
	const iterations = 2000
	r := rand.New(rand.NewSource(99))

	for it := 0; it < iterations; it++ {
		n := 1 + r.Intn(6)
		var rows []interface{}
		type best struct {
			ord  int
			val  float64
			name string
			set  bool
		}
		var hi, lo best
		for i := 0; i < n; i++ {
			v := r.Intn(100000)
			name := fmt.Sprintf("n%d", i)
			rows = append(rows, map[string]interface{}{"Score__c": fmt.Sprintf("%d", v), "Name__c": name})
			fv := float64(v)
			if !hi.set || fv > hi.val {
				hi = best{i, fv, name, true}
			}
			if !lo.set || fv < lo.val {
				lo = best{i, fv, name, true}
			}
		}
		gotHi := evalSelect(t, map[string]interface{}{"rows": rows}, "rows", "", "", "Score__c", "desc", "Name__c")
		if gotHi != hi.name {
			t.Fatalf("iter=%d desc expected %q got %v; rows=%v", it, hi.name, gotHi, rows)
		}
		gotLo := evalSelect(t, map[string]interface{}{"rows": rows}, "rows", "", "", "Score__c", "asc", "Name__c")
		if gotLo != lo.name {
			t.Fatalf("iter=%d asc expected %q got %v; rows=%v", it, lo.name, gotLo, rows)
		}
	}
}

// ---------------------------------------------------------------------------
// Fuzz: never panics / errors regardless of param strings and messy data.
// ---------------------------------------------------------------------------

func FuzzSelectByFieldSorted(f *testing.F) {
	f.Add("Multibureau__r.Bureau__c", "CRIF", "Multibureau_Date__c", "desc", "Multibureau__r.Credit_Bureau_pdf__c")
	f.Add("", "", "Score__c", "asc", "")
	f.Add("a.b.c", "x", "a.b.d", "DESC", "a.b.e")
	f.Add("'", "\"", "[0]", "asc", "..")
	f.Add(strings.Repeat("a.", 50), "v", "k", "desc", "x")

	f.Fuzz(func(t *testing.T, matchField, matchVal, sortField, order, extractField string) {
		// A payload mixing valid records, nulls, wrong types, and nested shapes.
		payload := map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CRIF", "2026-01-01T00:00:00.000+0000", "2026-01-01T00:00:00.000+0000", "pdf-a"),
				crifRecord("r2", "CRIF", nil, "", nil),
				crifRecord("r3", "CIBIL", "2026-09-09T00:00:00.000+0000", "", "pdf-c"),
				map[string]interface{}{"Id": "r4"},      // sparse record
				"not-an-object",                         // non-map entry
				map[string]interface{}{"Score__c": 123}, // numeric field
			},
		}
		// Sanitize params so they form VALID single-quoted literals; the
		// function's data-handling robustness (not govaluate's lexer) is what
		// we're fuzzing. Note: govaluate treats ' and " as interchangeable
		// string delimiters, so a literal cannot contain either quote char, and
		// {}/backslash would break the {{...}} envelope — strip them all.
		clean := func(s string) string {
			s = strings.ReplaceAll(s, "'", "")
			s = strings.ReplaceAll(s, "\"", "")
			s = strings.ReplaceAll(s, "{", "")
			s = strings.ReplaceAll(s, "}", "")
			s = strings.ReplaceAll(s, "\\", "")
			return s
		}
		expr := fmt.Sprintf("{{selectByFieldSorted(((records)),'%s','%s','%s','%s','%s')}}",
			clean(matchField), clean(matchVal), clean(sortField), clean(order), clean(extractField))
		if _, err := evaluateExpression(expr, payload, false, testLogger{}, nil); err != nil {
			t.Fatalf("selectByFieldSorted errored on params (%q,%q,%q,%q,%q): %v",
				matchField, matchVal, sortField, order, extractField, err)
		}
	})
}

// ---------------------------------------------------------------------------
// preProcessing storeAs -> _pp reference flow, and single vs double quote check
// ---------------------------------------------------------------------------

// The exact preprocessing expression from the user's config.
const ppLatestCrifExpr = `{{selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.Credit_Bureau_pdf__c')}}`

// runPreprocessThenBody mirrors ApplyPreprocessings (valueJson[storeAs]=result)
// then evaluates a body expression, so the _pp reference goes through the real
// param-map construction. A benign else avoids invoking the real uploadToS3.
func runPreprocessThenBody(t *testing.T, payload map[string]interface{}, bodyExpr string) interface{} {
	t.Helper()
	res, err := evaluateExpression(ppLatestCrifExpr, payload, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("preprocessing expr error: %v", err)
	}
	payload["_ppLatestCrifPdf"] = res // exactly what valueJson[pp.StoreAs] = result does
	got, err := evaluateExpression(bodyExpr, payload, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("body expr error: %v", err)
	}
	return got
}

func TestSelect_PreprocessStoreAs_PresentPassesThrough(t *testing.T) {
	payload := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CRIF", "2026-01-01T00:00:00.000+0000", "", "pdf-old"),
				crifRecord("r2", "CRIF", "2026-09-09T00:00:00.000+0000", "", "pdf-latest"),
			},
		},
	}
	// benign else stands in for uploadToS3(...)
	body := `{{_ppLatestCrifPdf!=''?_ppLatestCrifPdf:'FELL_THROUGH'}}`
	if got := runPreprocessThenBody(t, payload, body); got != "pdf-latest" {
		t.Fatalf("expected pass-through pdf-latest, got %v", got)
	}
}

func TestSelect_PreprocessStoreAs_NilRoutesToElse(t *testing.T) {
	// no CRIF -> selectByFieldSorted returns nil -> stored as nil
	payloadNoCrif := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CIBIL", "2026-09-09T00:00:00.000+0000", "", "pdf-cibil"),
			},
		},
	}
	body := `{{_ppLatestCrifPdf!=''?_ppLatestCrifPdf:'FELL_THROUGH'}}`
	if got := runPreprocessThenBody(t, payloadNoCrif, body); got != "FELL_THROUGH" {
		t.Fatalf("no-CRIF: expected else branch, got %v", got)
	}

	// latest CRIF has null pdf -> nil -> else
	payloadNullPdf := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CRIF", "2026-09-09T00:00:00.000+0000", "", nil),
			},
		},
	}
	if got := runPreprocessThenBody(t, payloadNullPdf, body); got != "FELL_THROUGH" {
		t.Fatalf("null-pdf: expected else branch, got %v", got)
	}
}

// ” (single) and "" (double) empty-string literals must behave identically,
// for both the present and the nil-stored cases.
func TestSelect_SingleVsDoubleQuoteEquivalence(t *testing.T) {
	cases := []struct {
		name    string
		records []interface{}
		want    string
	}{
		{
			name: "present",
			records: []interface{}{
				crifRecord("r1", "CRIF", "2026-09-09T00:00:00.000+0000", "", "pdf-latest"),
			},
			want: "pdf-latest",
		},
		{
			name: "nil",
			records: []interface{}{
				crifRecord("r1", "CIBIL", "2026-09-09T00:00:00.000+0000", "", "pdf-cibil"),
			},
			want: "FELL_THROUGH",
		},
	}
	for _, c := range cases {
		mk := func() map[string]interface{} {
			return map[string]interface{}{
				"multibureau_consolidate_data__c": map[string]interface{}{"records": c.records},
			}
		}
		single := runPreprocessThenBody(t, mk(), `{{_ppLatestCrifPdf!=''?_ppLatestCrifPdf:'FELL_THROUGH'}}`)
		double := runPreprocessThenBody(t, mk(), `{{_ppLatestCrifPdf!=""?_ppLatestCrifPdf:'FELL_THROUGH'}}`)
		if single != c.want || double != c.want {
			t.Fatalf("%s: want %q; single-quote got %v, double-quote got %v", c.name, c.want, single, double)
		}
		if single != double {
			t.Fatalf("%s: single(%v) != double(%v) — quotes not equivalent", c.name, single, double)
		}
	}
}

// Extract field == sort field: latest CIBIL Multibureau_Date__c value.
func TestSelect_LatestCibilDate_ExtractEqualsSort(t *testing.T) {
	payload := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CIBIL", "2026-02-02T00:00:00.000+0000", "", "pdf-a"),
				crifRecord("r2", "CIBIL", "2026-08-08T00:00:00.000+0000", "", "pdf-b"), // latest CIBIL
				crifRecord("r3", "CIBIL", nil, "", "pdf-c"),                            // null date, dropped
				crifRecord("r4", "CRIF", "2026-12-31T00:00:00.000+0000", "", "pdf-d"),  // later but wrong bureau
			},
		},
	}
	expr := `{{selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CIBIL','Multibureau_Date__c','desc','Multibureau_Date__c')}}`
	got, err := evaluateExpression(expr, payload, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != "2026-08-08T00:00:00.000+0000" {
		t.Fatalf("expected latest CIBIL date 2026-08-08..., got %v", got)
	}
}

// All CIBIL dates null -> nil.
func TestSelect_LatestCibilDate_AllNull(t *testing.T) {
	payload := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"records": []interface{}{
				crifRecord("r1", "CIBIL", nil, "", "pdf-a"),
				crifRecord("r2", "CIBIL", "", "", "pdf-b"),
			},
		},
	}
	expr := `{{selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CIBIL','Multibureau_Date__c','desc','Multibureau_Date__c')}}`
	got, err := evaluateExpression(expr, payload, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil when all CIBIL dates null, got %v", got)
	}
}

// Reproduces the reported case: multibureau_consolidate_data__c is the RECORD
// object directly (no "records" wrapper). Shows why ".records" -> html branch,
// and that dropping ".records" fixes it.
func TestSelect_ConsolidateIsDirectObject_NoRecordsKey(t *testing.T) {
	directObj := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{
			"Id":                  "a4z9H000000oyPBQAY",
			"Multibureau_Date__c": "2026-07-24T05:35:24.000+0000",
			"PAN__c":              "CEEEP5555E",
			"Multibureau__r": map[string]interface{}{
				"Bureau__c":            "CRIF",
				"CreatedDate":          "2026-07-24T05:35:25.000+0000",
				"Credit_Bureau_pdf__c": "https://example-bucket.s3.ap-south-1.amazonaws.com/Crif_PDF/x/y.html",
			},
		},
	}

	// (A) current config path -> looks for ".records" that doesn't exist -> nil
	wrong := `{{selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.Credit_Bureau_pdf__c')}}`
	gotWrong, err := evaluateExpression(wrong, directObj, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("wrong-path error: %v", err)
	}
	if gotWrong != nil {
		t.Fatalf("expected nil for .records path on direct object (this is why html runs), got %v", gotWrong)
	}

	// (B) point at the object itself -> single element -> returns the pdf
	right := `{{selectByFieldSorted(((multibureau_consolidate_data__c)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.Credit_Bureau_pdf__c')}}`
	gotRight, err := evaluateExpression(right, directObj, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("right-path error: %v", err)
	}
	if gotRight != "https://example-bucket.s3.ap-south-1.amazonaws.com/Crif_PDF/x/y.html" {
		t.Fatalf("expected the pdf url, got %v", gotRight)
	}
}

// Verifies the user's proposed expression is malformed, and that the corrected
// single-{{}} coalesce form handles BOTH shapes (records wrapper + direct object).
func TestSelect_DualShapeCoalesce(t *testing.T) {
	rec := func() map[string]interface{} {
		return crifRecord("r1", "CRIF", "2026-07-24T05:35:24.000+0000", "2026-07-24T05:35:25.000+0000", "pdf-x")
	}

	recordsShape := map[string]interface{}{
		"multibureau_consolidate_data__c": map[string]interface{}{"records": []interface{}{rec()}},
	}
	directShape := map[string]interface{}{
		"multibureau_consolidate_data__c": rec(),
	}

	// (A) user's proposed expression -> malformed (no leading {{, inner {{}}).
	broken := `((multibureau_consolidate_data__c.records))!=''?{{selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.CreatedDate')}}:{{selectByFieldSorted(((multibureau_consolidate_data__c)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.CreatedDate')}}`
	if _, err := evaluateExpression(broken, recordsShape, false, testLogger{}, nil); err == nil {
		t.Fatalf("expected a parse error for the malformed nested-brace expression, got none")
	}

	// (B) corrected single-{{}} coalesce.
	fixed := `{{selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.CreatedDate')!=''?selectByFieldSorted(((multibureau_consolidate_data__c.records)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.CreatedDate'):selectByFieldSorted(((multibureau_consolidate_data__c)),'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc','Multibureau__r.CreatedDate')}}`

	gotRecords, err := evaluateExpression(fixed, recordsShape, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("records-shape error: %v", err)
	}
	if gotRecords != "2026-07-24T05:35:25.000+0000" {
		t.Fatalf("records shape: expected CreatedDate, got %v", gotRecords)
	}

	gotDirect, err := evaluateExpression(fixed, directShape, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("direct-shape error: %v", err)
	}
	if gotDirect != "2026-07-24T05:35:25.000+0000" {
		t.Fatalf("direct shape: expected CreatedDate, got %v", gotDirect)
	}
}
