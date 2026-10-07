package utility

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// Production-readiness regression + fuzz suite for the Multibureau_Date__c
// interpolation expression:
//
//	{{multibureau_data__c.CreatedDate!=""?multibureau_data__c.CreatedDate:formatDate(now(),'YYYY-MM-DDTHH:mm:ss.SSSZ')}}
//
// Invariant under test (the contract we ship to prod):
//   - CreatedDate present AND a non-empty string -> output == that exact string
//   - CreatedDate empty string / explicit null / key missing / object empty /
//     object absent                              -> output == formatDate(now(), ...)
//
// The randomized test embeds the expression in large, realistic ESA-shaped
// payloads (sibling empty objects, contact/lead noise with null fields, deep
// nesting) so the flattener/resolver is exercised under production-like shapes.

// nowToleranceMinutes bounds how far the now() branch output may drift from the
// test clock. Generous enough to survive slow CI, tight enough to catch a
// stale/garbage value being emitted instead of a real timestamp.
const nowToleranceMinutes = 10

// assertNowBranch validates that s is a formatDate(now(), ...) output:
// matches the ISO layout, parses via the same parser the code uses, and is
// within tolerance of the current time.
func assertNowBranch(t *testing.T, s string, ctx string) {
	t.Helper()
	if !nowBranchPattern.MatchString(s) {
		t.Fatalf("[%s] expected now() ISO output, got %q", ctx, s)
	}
	parsed, err := parseFlexibleDate(s)
	if err != nil {
		t.Fatalf("[%s] now() output %q failed to parse: %v", ctx, s, err)
	}
	drift := time.Since(parsed.UTC())
	if drift < 0 {
		drift = -drift
	}
	if drift > nowToleranceMinutes*time.Minute {
		t.Fatalf("[%s] now() output %q drifted %v from wall clock", ctx, s, drift)
	}
}

// --- random data generators -------------------------------------------------

const asciiPunct = " \t!@#$%^&*()-_=+[]{};:,.<>/?|`~'\"\\"

func randomNonEmptyString(r *rand.Rand) string {
	n := 1 + r.Intn(40)
	var b strings.Builder
	for i := 0; i < n; i++ {
		switch r.Intn(5) {
		case 0:
			b.WriteByte(byte('a' + r.Intn(26)))
		case 1:
			b.WriteByte(byte('A' + r.Intn(26)))
		case 2:
			b.WriteByte(byte('0' + r.Intn(10)))
		case 3:
			b.WriteByte(asciiPunct[r.Intn(len(asciiPunct))])
		case 4:
			// occasional unicode to prove byte-exact pass-through
			runes := []rune{'é', 'ñ', 'ü', '中', '日', '₹', '—', '•'}
			b.WriteRune(runes[r.Intn(len(runes))])
		}
	}
	s := b.String()
	if s == "" { // defensive; loop guarantees n>=1 but chars could be empty runes
		return "x"
	}
	return s
}

var sfDateLayouts = []string{
	"2006-01-02T15:04:05.000-0700",
	"2006-01-02T15:04:05.000Z0700",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"02/01/2006",
	"01/02/2006 03:04:05 PM",
}

func randomDateString(r *rand.Rand) string {
	// Random instant within +/- ~15 years of now.
	delta := time.Duration(r.Int63n(int64(15*365*24*time.Hour))) - 15*365*24*time.Hour/2
	tm := time.Now().Add(delta).UTC()
	layout := sfDateLayouts[r.Intn(len(sfDateLayouts))]
	return tm.Format(layout)
}

// randomNoise merges realistic ESA-shaped clutter into the payload so the
// resolver is exercised against large/varied structures. Never writes the
// multibureau_data__c key (the scenario owns that).
func randomNoise(r *rand.Rand, payload map[string]interface{}) {
	// Sibling empty objects, exactly as ESA renders child sObjects with no record.
	emptySiblings := []string{
		"bank_facilities__c", "city_tier__c", "dealer_info__c", "income_model__c",
		"postal_code__c", "product_misc_detail__c", "transaction_data__c", "transaction_detail__c",
	}
	for _, k := range emptySiblings {
		if r.Intn(2) == 0 {
			payload[k] = map[string]interface{}{}
		}
	}

	// A contact object carrying a mix of real values and explicit nulls.
	if r.Intn(2) == 0 {
		contact := map[string]interface{}{
			"Id":                 "0039H00000LvM1KQAV",
			"Aadhaar_Number__c":  nil,
			"Age1__c":            18 + r.Intn(60),
			"Email":              "user@example.com",
			"CreatedDate":        randomDateString(r), // decoy: different object's CreatedDate must NOT leak
			"EmploymentType__c":  nil,
			"Monthly_Income__c":  nil,
		}
		payload["contact"] = contact
	}

	// A lead with its own CreatedDate (another decoy) and nested campaign.
	if r.Intn(2) == 0 {
		payload["lead"] = map[string]interface{}{
			"Id":          "00Q9H00000GaxhuUAB",
			"CreatedDate": randomDateString(r), // decoy
			"Campaign__r": map[string]interface{}{
				"Bscore__c":         nil,
				"Offer_Upgrade_Flag__c": r.Intn(2) == 0,
			},
		}
	}

	// A records-style array wrapper (like a_score__c.records) with deep nesting.
	if r.Intn(2) == 0 {
		recs := make([]interface{}, r.Intn(4))
		for i := range recs {
			recs[i] = map[string]interface{}{
				"Id":    fmt.Sprintf("a2z%09d", r.Intn(1_000_000_000)),
				"Score__c": nil,
				"Type__c":  randomNonEmptyString(r),
			}
		}
		payload["a_score__c"] = map[string]interface{}{"records": recs}
	}

	// A few arbitrary scalar noise keys.
	for i := 0; i < r.Intn(4); i++ {
		payload[fmt.Sprintf("noise_field_%d", i)] = randomNonEmptyString(r)
	}
}

// objectWithExtraFields returns a multibureau object populated with random
// non-CreatedDate fields (never adds a CreatedDate key itself).
func objectWithExtraFields(r *rand.Rand) map[string]interface{} {
	obj := map[string]interface{}{
		"Id":          fmt.Sprintf("a1k9H%09d", r.Intn(1_000_000_000)),
		"Response__c": randomNonEmptyString(r),
		"Type__c":     "CRIF",
	}
	if r.Intn(2) == 0 {
		obj["attributes"] = map[string]interface{}{"type": "Multibureau_Data__c"}
	}
	return obj
}

// scenarioKind enumerates the CreatedDate states we must handle in prod.
type scenarioKind int

const (
	kindPresentValidDate scenarioKind = iota
	kindPresentRandomNonEmpty
	kindEmptyString
	kindNull
	kindKeyMissing
	kindEmptyObject
	kindObjectAbsent
	kindCount
)

func (k scenarioKind) String() string {
	switch k {
	case kindPresentValidDate:
		return "present_valid_date"
	case kindPresentRandomNonEmpty:
		return "present_random_nonempty"
	case kindEmptyString:
		return "empty_string"
	case kindNull:
		return "null"
	case kindKeyMissing:
		return "key_missing"
	case kindEmptyObject:
		return "empty_object"
	case kindObjectAbsent:
		return "object_absent"
	default:
		return "unknown"
	}
}

// TestMultibureauDate_Regression_Randomized runs >5000 randomized cases across
// every scenario, embedded in realistic ESA-shaped payloads.
func TestMultibureauDate_Regression_Randomized(t *testing.T) {
	const iterations = 6000
	const seed = int64(0xC0FFEE) // deterministic for reproducible CI failures
	r := rand.New(rand.NewSource(seed))

	counts := make(map[scenarioKind]int, int(kindCount))

	for i := 0; i < iterations; i++ {
		payload := map[string]interface{}{}
		randomNoise(r, payload)

		kind := scenarioKind(r.Intn(int(kindCount)))
		counts[kind]++

		var expectPassthrough bool
		var expected string

		switch kind {
		case kindPresentValidDate:
			expected = randomDateString(r)
			obj := objectWithExtraFields(r)
			obj["CreatedDate"] = expected
			payload["multibureau_data__c"] = obj
			expectPassthrough = true

		case kindPresentRandomNonEmpty:
			expected = randomNonEmptyString(r)
			obj := objectWithExtraFields(r)
			obj["CreatedDate"] = expected
			payload["multibureau_data__c"] = obj
			expectPassthrough = true

		case kindEmptyString:
			obj := objectWithExtraFields(r)
			obj["CreatedDate"] = ""
			payload["multibureau_data__c"] = obj

		case kindNull:
			obj := objectWithExtraFields(r)
			obj["CreatedDate"] = nil // explicit JSON null
			payload["multibureau_data__c"] = obj

		case kindKeyMissing:
			payload["multibureau_data__c"] = objectWithExtraFields(r) // no CreatedDate

		case kindEmptyObject:
			payload["multibureau_data__c"] = map[string]interface{}{}

		case kindObjectAbsent:
			// intentionally do not set multibureau_data__c
		}

		ctx := fmt.Sprintf("iter=%d kind=%s", i, kind)
		got, err := evaluateExpression(multibureauDateExpr, payload, false, testLogger{}, nil)
		if err != nil {
			t.Fatalf("[%s] unexpected error: %v", ctx, err)
		}
		s, ok := got.(string)
		if !ok {
			t.Fatalf("[%s] expected string result, got %T (%v)", ctx, got, got)
		}

		if expectPassthrough {
			if s != expected {
				t.Fatalf("[%s] expected exact pass-through %q, got %q", ctx, expected, s)
			}
		} else {
			assertNowBranch(t, s, ctx)
		}
	}

	// Sanity: every scenario category was exercised a meaningful number of times.
	for k := scenarioKind(0); k < kindCount; k++ {
		if counts[k] == 0 {
			t.Fatalf("scenario %s was never generated across %d iterations", k, iterations)
		}
	}
	t.Logf("regression complete: %d iterations, distribution: %v", iterations, counts)
}

// TestMultibureauDate_Regression_DateFormatMatrix exhaustively checks that a
// present CreatedDate in any of the SF-observed date formats passes through
// byte-for-byte (never reformatted), across many random instants.
func TestMultibureauDate_Regression_DateFormatMatrix(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 700; i++ {
		for _, layout := range sfDateLayouts {
			delta := time.Duration(r.Int63n(int64(20 * 365 * 24 * time.Hour)))
			val := time.Now().Add(-delta).UTC().Format(layout)

			payload := map[string]interface{}{
				"multibureau_data__c": map[string]interface{}{"CreatedDate": val},
			}
			got, err := evaluateExpression(multibureauDateExpr, payload, false, testLogger{}, nil)
			if err != nil {
				t.Fatalf("layout=%q val=%q error: %v", layout, val, err)
			}
			if got != val {
				t.Fatalf("layout=%q expected pass-through %q, got %q", layout, val, got)
			}
		}
	}
}

// FuzzMultibureauDate fuzzes the CreatedDate string domain.
// Contract: empty string -> now(); any non-empty string -> exact pass-through.
func FuzzMultibureauDate(f *testing.F) {
	seeds := []string{
		"",
		" ",
		"2026-05-21T08:14:38.000+0000",
		"2026-05-21",
		"not-a-date",
		"null",
		"0",
		"\"quoted\"",
		"'single'",
		"multi\nline",
		"中文日期",
		strings.Repeat("x", 500),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, created string) {
		payload := map[string]interface{}{
			"multibureau_data__c": map[string]interface{}{
				"Id":          "a1k9H00000dQqMIQA0",
				"CreatedDate": created,
			},
		}
		got, err := evaluateExpression(multibureauDateExpr, payload, false, testLogger{}, nil)
		if err != nil {
			t.Fatalf("unexpected error for CreatedDate=%q: %v", created, err)
		}
		s, ok := got.(string)
		if !ok {
			t.Fatalf("expected string result for CreatedDate=%q, got %T", created, got)
		}

		if created == "" {
			assertNowBranch(t, s, fmt.Sprintf("fuzz empty CreatedDate=%q", created))
			return
		}
		if s != created {
			t.Fatalf("expected exact pass-through for CreatedDate=%q, got %q", created, s)
		}
	})
}
