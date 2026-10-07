package utility

import (
	"fmt"
	"math"
	"strconv"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func mustGetFn(t *testing.T, name string) func(...interface{}) (interface{}, error) {
	t.Helper()
	fn, ok := customFunctions[name]
	if !ok {
		t.Fatalf("custom function %q not registered", name)
	}
	return fn
}

func callInt(t *testing.T, fn func(...interface{}) (interface{}, error), args ...interface{}) int {
	t.Helper()
	got, err := fn(args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n, ok := got.(int)
	if !ok {
		t.Fatalf("expected int, got %T (%v)", got, got)
	}
	return n
}

func callFloat(t *testing.T, fn func(...interface{}) (interface{}, error), args ...interface{}) float64 {
	t.Helper()
	got, err := fn(args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	f, ok := got.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T (%v)", got, got)
	}
	return f
}

func callStr(t *testing.T, fn func(...interface{}) (interface{}, error), args ...interface{}) string {
	t.Helper()
	got, err := fn(args...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := got.(string)
	if !ok {
		t.Fatalf("expected string, got %T (%v)", got, got)
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// stripToDigits
// ─────────────────────────────────────────────────────────────────────────────

func TestStripToDigits_TableDriven(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"0", "0"},
		{"123", "123"},
		{"12.34", "1234"},
		{"1,234", "1234"},
		{"₹1,234", "1234"},
		{"$20,000/weekly", "20000"},
		{"abc", ""},
		{"  42  ", "42"},
		{"-123", "123"},
		{"1.2.3", "123"},
		{"0000", "0000"},
		{"9999999999", "9999999999"},
		{"abc123def456", "123456"},
		{"!@#$%^&*()", ""},
		{"1 2 3", "123"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("stripToDigits(%q)", tc.in), func(t *testing.T) {
			got := stripToDigits(tc.in)
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestStripToDigitsAndDot_TableDriven(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"0", "0"},
		{"3.14", "3.14"},
		{"1,234.56", "1234.56"},
		{"1,234.56/month", "1234.56"},
		{"₹ 1,234.56", "1234.56"},
		{"$20,000/weekly", "20000"},
		{"abc", ""},
		{"  3.14  ", "3.14"}, // spaces stripped, dot preserved
		{"-3.14", "3.14"},
		{"1.2.3", "1.23"},
		{".", "."},
		{".5", ".5"},
		{"5.", "5."},
		{"0.0", "0.0"},
		{"abc1.2def", "1.2"},
		{"1,000,000.99", "1000000.99"},
		{"!@#$%^", ""},
		{"1 2 . 3 4", "12.34"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("stripToDigitsAndDot(%q)", tc.in), func(t *testing.T) {
			got := stripToDigitsAndDot(tc.in)
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// stringToInt — explicit scenario table
// ─────────────────────────────────────────────────────────────────────────────

func TestStringToInt_TableDriven(t *testing.T) {
	fn := mustGetFn(t, "stringToInt")

	cases := []struct {
		name string
		in   interface{}
		want int
	}{
		// nil / empty
		{"nil input", nil, 0},
		{"empty string", "", 0},
		{"whitespace only", "   ", 0},

		// plain integers
		{"zero", "0", 0},
		{"positive", "42", 42},
		{"large", "1000000", 1000000},
		{"leading zeros", "007", 7},
		{"all nines", "9999", 9999},

		// formatted with commas
		{"comma thousands", "1,000", 1000},
		{"comma millions", "1,000,000", 1000000},
		{"INR formatted", "₹1,00,000", 100000},
		{"currency prefix", "$20,000", 20000},
		{"currency with slash unit", "20,000/weekly", 20000},
		// "Rs." contains a dot before any digit, so stripToDigitsAndDot yields ".5000"
		// which ParseFloat parses as 0.5 → int(0.5) = 0. Known limitation: prefixes
		// that contain a dot consume the decimal slot. Use stringToDouble for such inputs.
		{"Rs prefix dot before digits", "Rs. 5,000", 0},
		{"EUR prefix", "€ 9,999", 9999},

		// decimal strings (truncated)
		{"decimal truncated", "3.99", 3},
		{"decimal zero frac", "10.0", 10},
		{"decimal large", "1,234.56", 1234},
		{"decimal with unit", "1,234.56/month", 1234},
		{"only dot", ".", 0},
		{"dot then digit", ".5", 0},
		{"digit then dot", "5.", 5},

		// negative (minus stripped, only digits kept)
		{"negative int", "-100", 100},
		{"negative decimal", "-3.14", 3},

		// alpha input → 0
		{"pure alpha", "abc", 0},
		{"mixed alpha", "abc100def", 100},
		{"all special chars", "!@#$%^&*()", 0},

		// numeric types passed directly
		{"int type", 42, 42},
		{"int32 type", int32(100), 100},
		{"int64 type", int64(200), 200},
		{"float32 type", float32(3.9), 3},
		{"float64 type", float64(7.1), 7},

		// whitespace around number
		{"leading space", "  42  ", 42},
		{"tab around", "\t100\t", 100},

		// zero variants
		{"zero string", "0", 0},
		{"zero float str", "0.0", 0},
		{"zero comma", "0,000", 0},

		// edge numbers
		{"max safe uint32 str", "4294967295", 4294967295},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callInt(t, fn, tc.in)
			if got != tc.want {
				t.Errorf("stringToInt(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// stringToInt — regression: plain integers 0..2499
// ─────────────────────────────────────────────────────────────────────────────

func TestStringToInt_Regression_PlainIntegers(t *testing.T) {
	fn := mustGetFn(t, "stringToInt")
	for i := 0; i < 2500; i++ {
		s := strconv.Itoa(i)
		got := callInt(t, fn, s)
		if got != i {
			t.Errorf("stringToInt(%q) = %d, want %d", s, got, i)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// stringToInt — regression: comma-formatted integers 0..2499
// ─────────────────────────────────────────────────────────────────────────────

func TestStringToInt_Regression_CommaFormattedIntegers(t *testing.T) {
	fn := mustGetFn(t, "stringToInt")
	for i := 0; i < 2500; i++ {
		// Build a comma-formatted string like "1,234"
		raw := strconv.Itoa(i)
		formatted := insertCommas(raw)
		got := callInt(t, fn, formatted)
		if got != i {
			t.Errorf("stringToInt(%q) = %d, want %d", formatted, got, i)
		}
	}
}

// insertCommas formats an integer string with thousands separators.
func insertCommas(s string) string {
	if len(s) <= 3 {
		return s
	}
	mod := len(s) % 3
	out := s[:mod]
	for i := mod; i < len(s); i += 3 {
		if i > 0 {
			out += ","
		}
		out += s[i : i+3]
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// stringToDouble — explicit scenario table
// ─────────────────────────────────────────────────────────────────────────────

func TestStringToDouble_TableDriven(t *testing.T) {
	fn := mustGetFn(t, "stringToDouble")

	cases := []struct {
		name string
		in   interface{}
		want float64
	}{
		// nil / empty
		{"nil input", nil, 0},
		{"empty string", "", 0},
		{"whitespace only", "   ", 0},
		{"dot only", ".", 0},

		// plain floats
		{"zero", "0", 0},
		{"pi", "3.14", 3.14},
		{"integer string", "42", 42},
		{"zero decimal", "0.0", 0},
		{"one decimal", "1.5", 1.5},
		{"negative (sign stripped)", "-3.14", 3.14},
		{"leading zeros", "007.5", 7.5},

		// formatted with commas
		{"comma thousands int", "1,000", 1000},
		{"comma millions int", "1,000,000", 1000000},
		{"comma float", "1,234.56", 1234.56},
		{"comma float with unit", "1,234.56/month", 1234.56},
		{"INR formatted", "₹1,00,000.50", 100000.50},
		{"currency prefix", "$20,000.99", 20000.99},
		{"EUR prefix", "€ 9,999.01", 9999.01},

		// unit suffixes
		{"weekly suffix", "20,000/weekly", 20000},
		{"px suffix", "100px", 100},
		{"km suffix", "12.5 km", 12.5},
		{"percent suffix", "85.5%", 85.5},

		// multiple dots (only first kept)
		{"two dots", "1.2.3", 1.23},
		{"three dots", "1.2.3.4", 1.234},

		// alpha input
		{"pure alpha", "abc", 0},
		{"mixed alpha", "abc1.5def", 1.5},
		{"all special chars", "!@#$%^&*()", 0},

		// numeric types passed directly
		{"float64 type", float64(3.14), 3.14},
		{"float32 type", float32(1.5), float64(float32(1.5))},
		{"int type", 42, 42},
		{"int32 type", int32(100), 100},
		{"int64 type", int64(200), 200},

		// whitespace
		{"leading space", "  3.14  ", 3.14},

		// zero variants
		{"zero comma", "0,000", 0},
		{"zero float str", "0.00", 0},

		// dot-prefixed
		{".5 string", ".5", 0.5},
		{"5. string", "5.", 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callFloat(t, fn, tc.in)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("stringToDouble(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// stringToDouble — regression: plain floats x.xx for x in 0..49, decimals 0..99
// (50 * 100 = 5000 cases)
// ─────────────────────────────────────────────────────────────────────────────

func TestStringToDouble_Regression_PlainFloats(t *testing.T) {
	fn := mustGetFn(t, "stringToDouble")
	for whole := 0; whole < 50; whole++ {
		for frac := 0; frac < 100; frac++ {
			want := float64(whole) + float64(frac)/100.0
			s := fmt.Sprintf("%d.%02d", whole, frac)
			got := callFloat(t, fn, s)
			if math.Abs(got-want) > 1e-9 {
				t.Errorf("stringToDouble(%q) = %v, want %v", s, got, want)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// intToString — explicit scenario table
// ─────────────────────────────────────────────────────────────────────────────

func TestIntToString_TableDriven(t *testing.T) {
	fn := mustGetFn(t, "intToString")

	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{"nil input", nil, ""},
		{"zero int", 0, "0"},
		{"positive int", 42, "42"},
		{"negative int", -1, "-1"},
		{"large int", 1000000, "1000000"},
		{"int32", int32(100), "100"},
		{"int64", int64(999), "999"},
		{"float64 whole", float64(10), "10"},
		{"float64 truncated", float64(3.9), "3"},
		{"string passthrough", "hello", "hello"},
		{"bool passthrough", true, "true"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callStr(t, fn, tc.in)
			if got != tc.want {
				t.Errorf("intToString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// intToString — regression: round-trip with stringToInt for 0..2499
// ─────────────────────────────────────────────────────────────────────────────

func TestIntToString_Regression_RoundTrip(t *testing.T) {
	iFn := mustGetFn(t, "intToString")
	sFn := mustGetFn(t, "stringToInt")
	for i := 0; i < 2500; i++ {
		s := callStr(t, iFn, i)
		want := strconv.Itoa(i)
		if s != want {
			t.Errorf("intToString(%d) = %q, want %q", i, s, want)
		}
		back := callInt(t, sFn, s)
		if back != i {
			t.Errorf("stringToInt(intToString(%d)) = %d, want %d", i, back, i)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// doubleToString — explicit scenario table
// ─────────────────────────────────────────────────────────────────────────────

func TestDoubleToString_TableDriven(t *testing.T) {
	fn := mustGetFn(t, "doubleToString")

	cases := []struct {
		name string
		in   interface{}
		want string
	}{
		{"nil input", nil, ""},
		{"zero", float64(0), "0"},
		{"pi", 3.14, "3.14"},
		{"whole number", float64(10), "10"},
		{"negative", float64(-3.14), "-3.14"},
		{"large float", float64(1234567.89), "1234567.89"},
		{"int type", 42, "42"},
		{"int32 type", int32(100), "100"},
		{"int64 type", int64(200), "200"},
		{"float32 type", float32(1.5), "1.5"},
		{"trailing zero stripped", 5.10, "5.1"},
		{"multiple decimals", 1.23456789, "1.23456789"},
		{"string passthrough", "hello", "hello"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callStr(t, fn, tc.in)
			if got != tc.want {
				t.Errorf("doubleToString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// doubleToString — regression: round-trip with stringToDouble for x.xx (50×100)
// ─────────────────────────────────────────────────────────────────────────────

func TestDoubleToString_Regression_RoundTrip(t *testing.T) {
	dFn := mustGetFn(t, "doubleToString")
	sFn := mustGetFn(t, "stringToDouble")
	for whole := 0; whole < 50; whole++ {
		for frac := 0; frac < 100; frac++ {
			f := float64(whole) + float64(frac)/100.0
			s := callStr(t, dFn, f)
			back := callFloat(t, sFn, s)
			if math.Abs(back-f) > 1e-9 {
				t.Errorf("round-trip failed for %v: doubleToString=%q, stringToDouble back=%v", f, s, back)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// end-to-end via evaluateExpression
// ─────────────────────────────────────────────────────────────────────────────

func TestTypeConversions_ViaEvaluateExpression(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		valueJSON map[string]interface{}
		want    interface{}
	}{
		{
			name: "stringToInt plain",
			expr: "{{stringToInt(salary)}}",
			valueJSON: map[string]interface{}{"salary": "42000"},
			want: 42000,
		},
		{
			name: "stringToInt comma formatted",
			expr: "{{stringToInt(salary)}}",
			valueJSON: map[string]interface{}{"salary": "42,000"},
			want: 42000,
		},
		{
			name: "stringToInt with unit",
			expr: "{{stringToInt(income)}}",
			valueJSON: map[string]interface{}{"income": "20,000/weekly"},
			want: 20000,
		},
		{
			name: "stringToInt decimal truncated",
			expr: "{{stringToInt(score)}}",
			valueJSON: map[string]interface{}{"score": "1,234.56"},
			want: 1234,
		},
		{
			name: "stringToInt invalid returns 0",
			expr: "{{stringToInt(code)}}",
			valueJSON: map[string]interface{}{"code": "abc"},
			want: 0,
		},
		{
			name: "stringToDouble plain",
			expr: "{{stringToDouble(rate)}}",
			valueJSON: map[string]interface{}{"rate": "3.14"},
			want: 3.14,
		},
		{
			name: "stringToDouble comma float",
			expr: "{{stringToDouble(amount)}}",
			valueJSON: map[string]interface{}{"amount": "1,234.56"},
			want: 1234.56,
		},
		{
			name: "stringToDouble with suffix",
			expr: "{{stringToDouble(income)}}",
			valueJSON: map[string]interface{}{"income": "20,000/weekly"},
			want: float64(20000),
		},
		{
			name: "intToString basic",
			expr: "{{intToString(count)}}",
			valueJSON: map[string]interface{}{"count": 99},
			want: "99",
		},
		{
			name: "doubleToString basic",
			expr: "{{doubleToString(rate)}}",
			valueJSON: map[string]interface{}{"rate": 3.14},
			want: "3.14",
		},
		{
			name: "doubleToString whole number no trailing zero",
			expr: "{{doubleToString(val)}}",
			valueJSON: map[string]interface{}{"val": float64(10)},
			want: "10",
		},
		{
			name: "chained stringToInt then intToString",
			expr: "{{intToString(stringToInt(salary))}}",
			valueJSON: map[string]interface{}{"salary": "42,000/year"},
			want: "42000",
		},
		{
			name: "chained stringToDouble then doubleToString",
			expr: "{{doubleToString(stringToDouble(amount))}}",
			valueJSON: map[string]interface{}{"amount": "1,234.56/month"},
			want: "1234.56",
		},
		// NOTE: stringToInt returns Go int, which govaluate cannot use with +/-/*
		// arithmetic operators (govaluate requires float64 for arithmetic).
		// Use stringToDouble when the result must participate in arithmetic.
		{
			name: "stringToDouble arithmetic sum",
			expr: "{{stringToDouble(a) + stringToDouble(b)}}",
			valueJSON: map[string]interface{}{"a": "1,000.50", "b": "2,000.25"},
			want: 3000.75,
		},
		{
			name: "stringToDouble arithmetic comma int",
			expr: "{{stringToDouble(a) + stringToDouble(b)}}",
			valueJSON: map[string]interface{}{"a": "1,000", "b": "2,000"},
			want: float64(3000),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evaluateExpression(tc.expr, tc.valueJSON, false, testLogger{}, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch want := tc.want.(type) {
			case float64:
				gotF, ok := got.(float64)
				if !ok {
					t.Fatalf("expected float64, got %T (%v)", got, got)
				}
				if math.Abs(gotF-want) > 1e-9 {
					t.Errorf("expr=%q: got %v, want %v", tc.expr, gotF, want)
				}
			case int:
				// govaluate returns float64 for numeric results; int for custom fn results
				switch g := got.(type) {
				case int:
					if g != want {
						t.Errorf("expr=%q: got %d, want %d", tc.expr, g, want)
					}
				case float64:
					if int(g) != want {
						t.Errorf("expr=%q: got %v, want %d", tc.expr, g, want)
					}
				default:
					t.Errorf("expr=%q: unexpected type %T (%v)", tc.expr, got, got)
				}
			default:
				if got != tc.want {
					t.Errorf("expr=%q: got %v (%T), want %v (%T)", tc.expr, got, got, tc.want, tc.want)
				}
			}
		})
	}
}
