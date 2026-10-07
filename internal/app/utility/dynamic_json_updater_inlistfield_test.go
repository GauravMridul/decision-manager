package utility

import (
	"context"
	"testing"

	"github.com/dmi-infotech/common-modules/go/contracts"
)

type testLogger struct{}

func (l testLogger) Debug(msg string)                                          {}
func (l testLogger) Debugw(msg string, keysAndValues ...interface{})           {}
func (l testLogger) Debugf(format string, args ...interface{})                 {}
func (l testLogger) Info(args ...interface{})                                  {}
func (l testLogger) Infow(msg string, keysAndValues ...interface{})            {}
func (l testLogger) Infof(format string, args ...interface{})                  {}
func (l testLogger) Warn(msg string)                                           {}
func (l testLogger) Warnw(msg string, keysAndValues ...interface{})            {}
func (l testLogger) Warnf(format string, args ...interface{})                  {}
func (l testLogger) Error(msg string)                                          {}
func (l testLogger) Errorw(msg string, keysAndValues ...interface{})           {}
func (l testLogger) Errorf(format string, args ...interface{})                 {}
func (l testLogger) Fatal(msg string)                                          {}
func (l testLogger) Fatalf(format string, args ...interface{})                 {}
func (l testLogger) WithContext(ctx context.Context) contracts.Logger          { return l }
func (l testLogger) WithFields(fields map[string]interface{}) contracts.Logger { return l }

func TestInListField_NormalArrayArgs_Match(t *testing.T) {
	fn, ok := customFunctions["inListField"]
	if !ok {
		t.Fatal("inListField function not found")
	}

	accounts := []interface{}{
		map[string]interface{}{"accountType": "10"},
		map[string]interface{}{"accountType": "52"},
	}

	got, err := fn(accounts, "accountType", "12", "14", "52")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	match, ok := got.(bool)
	if !ok {
		t.Fatalf("expected bool result, got %T", got)
	}
	if !match {
		t.Fatalf("expected match=true, got false")
	}
}

func TestInListField_ExpandedArgs_Match(t *testing.T) {
	fn, ok := customFunctions["inListField"]
	if !ok {
		t.Fatal("inListField function not found")
	}

	got, err := fn(
		map[string]interface{}{"accountType": "12"},
		map[string]interface{}{"accountType": "52"},
		"accountType",
		"40",
		"52",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	match, ok := got.(bool)
	if !ok {
		t.Fatalf("expected bool result, got %T", got)
	}
	if !match {
		t.Fatalf("expected match=true, got false")
	}
}

func TestInListField_ExpandedArgs_NoMatch(t *testing.T) {
	fn, ok := customFunctions["inListField"]
	if !ok {
		t.Fatal("inListField function not found")
	}

	got, err := fn(
		map[string]interface{}{"accountType": "12"},
		map[string]interface{}{"accountType": "52"},
		"accountType",
		"71",
		"72",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	match, ok := got.(bool)
	if !ok {
		t.Fatalf("expected bool result, got %T", got)
	}
	if match {
		t.Fatalf("expected match=false, got true")
	}
}

func TestInListField_NormalArrayArgs_NumericFieldValue_Match(t *testing.T) {
	fn, ok := customFunctions["inListField"]
	if !ok {
		t.Fatal("inListField function not found")
	}

	accounts := []interface{}{
		map[string]interface{}{"accountType": int64(61)},
	}

	got, err := fn(accounts, "accountType", "61")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	match, ok := got.(bool)
	if !ok {
		t.Fatalf("expected bool result, got %T", got)
	}
	if !match {
		t.Fatalf("expected match=true, got false")
	}
}

func TestEvaluateExpression_InListFieldArrayArg_Match(t *testing.T) {
	valueJSON := map[string]interface{}{
		"accounts": []interface{}{
			map[string]interface{}{"accountType": "12"},
			map[string]interface{}{"accountType": "52"},
		},
	}

	expr := "{{inListField(((accounts)),'accountType','40','52')}}"
	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	match, ok := got.(bool)
	if !ok {
		t.Fatalf("expected bool result, got %T", got)
	}
	if !match {
		t.Fatalf("expected match=true, got false")
	}
}

func TestEvaluateExpression_InListFieldArrayArg_NoMatch(t *testing.T) {
	valueJSON := map[string]interface{}{
		"accounts": []interface{}{
			map[string]interface{}{"accountType": "12"},
			map[string]interface{}{"accountType": "52"},
		},
	}

	expr := "{{inListField(((accounts)),'accountType','40','41')}}"
	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	match, ok := got.(bool)
	if !ok {
		t.Fatalf("expected bool result, got %T", got)
	}
	if match {
		t.Fatalf("expected match=false, got true")
	}
}

func TestEvaluateExpression_NestedTernary_RequiresExplicitParentheses(t *testing.T) {
	valueJSON := map[string]interface{}{
		"gate": true,
		"a":    true,
		"b":    true,
	}

	// Explicit parentheses around nested ternary conditions/branches avoid
	// govaluate ambiguity where a non-bool branch value can be parsed as the
	// next ternary condition.
	expr := "{{((gate)==true)?(((a)==true)?'X':(((b)==true)?'Y':'Z')):null}}"
	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "X" {
		t.Fatalf("expected X, got %v", got)
	}
}
