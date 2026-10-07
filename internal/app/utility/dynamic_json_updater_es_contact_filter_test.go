package utility

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"decision-manager/internal/app/types"
)

func TestInterpolateValuesWithArrayExpansion_ESContactFilter_BankAA_FromFixture(t *testing.T) {
	updater := newBenchmarkUpdater()
	esaResponseBody := loadESAResponseBodyFixture(t)

	valueJSON, err := TransformESAResponse(esaResponseBody)
	if err != nil {
		// Fixture contains unescaped newlines inside string values (invalid JSON).
		// Retry with minimal sanitization while keeping production parsing logic.
		valueJSON, err = TransformESAResponse(escapeUnescapedNewlinesInJSONStringLiterals(esaResponseBody))
		if err != nil {
			t.Fatalf("failed to transform ESA response fixture: %v", err)
		}
	}

	mapping := []types.SFRequestDB{
		{
			URL:    "/services/data/v64.0/sobjects/ES_Contact__c/<current.Id>",
			Method: "POST",
			RefID:  "PANITRServiceKARZA_ES_Contact__c_Post_{{index}}",
			Body: map[string]interface{}{
				"Type__c":          "Pan ITR Service",
				"Contact__c":       "<contact.Id>",
				"Response__c":      "((PANITRServiceKARZA.response.body.result.message))",
				"Response_Status__c": "((PANITRServiceKARZA.response.body.statusCode))",
			},
			Merge:     "false",
			ArrayPath: "es_contact__c.records",
			FilterConditions: &types.FilterConditions{
				SelectWhen: "{{((current.Type__c)) == 'BankAA'}}",
			},
		},
	}

	got, err := updater.InterpolateValuesWithArrayExpansion(context.Background(), mapping, valueJSON)
	if err != nil {
		t.Fatalf("unexpected interpolation error: %v", err)
	}

	// Log final mapping generated from this configuration.
	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	t.Logf("final interpolated mapping: %s", gotJSON)

	// The fixture contains an ES_Contact__c record with Type__c = "BankAA",
	// so this filter should produce exactly one final mapped request.
	expected := []types.SFRequest{
		{
			URL:    "/services/data/v64.0/sobjects/ES_Contact__c/a00OW00000t8YbOYAU",
			Method: "POST",
			RefID:  "PANITRServiceKARZA_ES_Contact__c_Post_0",
			Body: map[string]interface{}{
				"Type__c":    "Pan ITR Service",
				"Contact__c": "003OW00000nC7CqYAK",
				// PANITRServiceKARZA paths are unresolved for this fixture, so
				// Response__c and Response_Status__c are omitted by design.
			},
		},
	}
	if !reflect.DeepEqual(got, expected) {
		expectedJSON, _ := json.MarshalIndent(expected, "", "  ")
		t.Fatalf("unexpected final mapping.\nexpected=%s\ngot=%s", expectedJSON, gotJSON)
	}
}

func loadESAResponseBodyFixture(t *testing.T) string {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to resolve test file path")
	}

	candidatePaths := []string{
		"esa_response.txt",
		filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "esa_response.txt"),
	}

	var raw []byte
	var readErr error
	for _, p := range candidatePaths {
		raw, readErr = os.ReadFile(p)
		if readErr == nil {
			break
		}
	}
	if readErr != nil {
		t.Fatalf("unable to read esa_response.txt fixture from known paths: %v", readErr)
	}

	return extractESAResponseBody(t, string(raw))
}

func extractESAResponseBody(t *testing.T, raw string) string {
	t.Helper()

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		t.Fatal("esa response fixture is empty")
	}

	// The fixture is typically a key-value fragment without surrounding braces.
	type wrapper struct {
		ESAResponseBody string `json:"esaResponseBody"`
	}

	var w wrapper
	if err := json.Unmarshal([]byte(trimmed), &w); err == nil && w.ESAResponseBody != "" {
		return w.ESAResponseBody
	}
	if err := json.Unmarshal([]byte("{"+trimmed+"}"), &w); err == nil && w.ESAResponseBody != "" {
		return w.ESAResponseBody
	}

	// Last-resort extraction for key-value fragment:
	// "esaResponseBody": "<escaped-json>",
	const prefix = `"esaResponseBody": "`
	start := strings.Index(trimmed, prefix)
	if start == -1 {
		t.Fatal("could not locate esaResponseBody prefix in fixture content")
	}
	start += len(prefix)

	end := strings.LastIndex(trimmed, `",`)
	if end <= start {
		// fallback if trailing comma is absent
		end = strings.LastIndex(trimmed, `"`)
	}
	if end <= start {
		t.Fatal("could not locate esaResponseBody suffix in fixture content")
	}

	payload := trimmed[start:end]
	unquoted, err := strconv.Unquote(`"` + payload + `"`)
	if err != nil {
		// In case fixture is already unescaped/plain JSON string content.
		if strings.HasPrefix(payload, "{") && strings.Contains(payload, `"data"`) {
			return payload
		}
		t.Fatalf("failed to unquote esaResponseBody payload: %v", err)
	}
	return unquoted
}

func escapeUnescapedNewlinesInJSONStringLiterals(input string) string {
	if input == "" {
		return input
	}

	var b strings.Builder
	b.Grow(len(input) + 64)

	inString := false
	escaped := false

	for i := 0; i < len(input); i++ {
		ch := input[i]

		if inString {
			if escaped {
				b.WriteByte(ch)
				escaped = false
				continue
			}

			switch ch {
			case '\\':
				b.WriteByte(ch)
				escaped = true
			case '"':
				b.WriteByte(ch)
				inString = false
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				// drop CR when found inside a string literal
			default:
				b.WriteByte(ch)
			}
			continue
		}

		if ch == '"' {
			inString = true
		}
		b.WriteByte(ch)
	}

	return b.String()
}
