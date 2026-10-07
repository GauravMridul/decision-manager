package utility

import "testing"

func TestLeftFunction_Basic(t *testing.T) {
	fn, ok := customFunctions["left"]
	if !ok {
		t.Fatal("left function not found")
	}

	got, err := fn("1234567890", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1234" {
		t.Fatalf("expected %q, got %v", "1234", got)
	}
}

func TestRightFunction_Basic(t *testing.T) {
	fn, ok := customFunctions["right"]
	if !ok {
		t.Fatal("right function not found")
	}

	got, err := fn("1234567890", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "7890" {
		t.Fatalf("expected %q, got %v", "7890", got)
	}
}

func TestSubstringFunction_StartOnly(t *testing.T) {
	fn, ok := customFunctions["substring"]
	if !ok {
		t.Fatal("substring function not found")
	}

	got, err := fn("ABCDE1234F", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1234F" {
		t.Fatalf("expected %q, got %v", "1234F", got)
	}
}

func TestSubstringFunction_StartAndLength(t *testing.T) {
	fn, ok := customFunctions["substring"]
	if !ok {
		t.Fatal("substring function not found")
	}

	got, err := fn("ABCDE1234F", 0, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "AB" {
		t.Fatalf("expected %q, got %v", "AB", got)
	}
}

func TestIntToStringFunction_Basic(t *testing.T) {
	fn, ok := customFunctions["intToString"]
	if !ok {
		t.Fatal("intToString function not found")
	}

	got, err := fn(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "42" {
		t.Fatalf("expected %q, got %v", "42", got)
	}
}

func TestStringToIntAliasFunction_InvalidInputDefaultsToZero(t *testing.T) {
	fn, ok := customFunctions["stringToInt"]
	if !ok {
		t.Fatal("stringToInt function not found")
	}

	got, err := fn("abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotInt, ok := got.(int)
	if !ok {
		t.Fatalf("expected int result, got %T", got)
	}
	if gotInt != 0 {
		t.Fatalf("expected %d, got %d", 0, gotInt)
	}
}

func TestStringToIntFunction_Basic(t *testing.T) {
	fn, ok := customFunctions["stringToInt"]
	if !ok {
		t.Fatal("stringToInt function not found")
	}

	got, err := fn("42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotInt, ok := got.(int)
	if !ok {
		t.Fatalf("expected int result, got %T", got)
	}
	if gotInt != 42 {
		t.Fatalf("expected %d, got %d", 42, gotInt)
	}
}

func TestEvaluateExpression_WithStringToIntAndIntToString(t *testing.T) {
	valueJSON := map[string]interface{}{
		"acticoData": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"body": map[string]interface{}{
						"crif": map[string]interface{}{
							"features": map[string]interface{}{
								"NUM_BUSINESS_LOANS": "2",
							},
						},
					},
				},
			},
		},
	}

	expr := "{{intToString(stringToInt(acticoData.response.body.body.crif.features.NUM_BUSINESS_LOANS))}}"
	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2" {
		t.Fatalf("expected %q, got %v", "2", got)
	}
}

func TestSubstringFunction_OutOfRangeStart_ReturnsEmpty(t *testing.T) {
	fn, ok := customFunctions["substring"]
	if !ok {
		t.Fatal("substring function not found")
	}

	got, err := fn("ABCD", 10, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty string, got %v", got)
	}
}

func TestEvaluateExpression_WithRightAndSubstring_AadhaarStyle(t *testing.T) {
	valueJSON := map[string]interface{}{
		"aadhaar_a": "XX1234567890",
		"aadhaar_b": "XX9876567890",
	}

	expr := "{{(toUpper(right(((aadhaar_a)),4))==toUpper(right(((aadhaar_b)),4)))&&((toUpper(substring(((aadhaar_a)),0,2)))=='XX')}}"
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

func TestTruncateFunction_String(t *testing.T) {
	fn, ok := customFunctions["truncate"]
	if !ok {
		t.Fatal("truncate function not found")
	}

	got, err := fn("abcdef", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "abcd" {
		t.Fatalf("expected %q, got %q", "abcd", gotStr)
	}
}

func TestTruncateFunction_Number_Unchanged(t *testing.T) {
	fn, ok := customFunctions["truncate"]
	if !ok {
		t.Fatal("truncate function not found")
	}

	got, err := fn(123456, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotNum, ok := got.(int)
	if !ok {
		t.Fatalf("expected int result, got %T", got)
	}
	if gotNum != 123456 {
		t.Fatalf("expected %d, got %d", 123456, gotNum)
	}
}

func TestTruncateFunction_ArrayAndObject_PreserveShape(t *testing.T) {
	fn, ok := customFunctions["truncate"]
	if !ok {
		t.Fatal("truncate function not found")
	}

	input := map[string]interface{}{
		"name":  "abcdefgh",
		"count": 12,
		"items": []interface{}{"123456", 7, map[string]interface{}{"desc": "uvwxyz"}},
	}

	got, err := fn(input, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj, ok := got.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	if obj["name"] != "abc" {
		t.Fatalf("expected truncated name %q, got %v", "abc", obj["name"])
	}
	if obj["count"] != 12 {
		t.Fatalf("expected count to remain 12, got %v", obj["count"])
	}

	items, ok := obj["items"].([]interface{})
	if !ok || len(items) != 3 {
		t.Fatalf("expected items slice len 3, got %T len %d", obj["items"], len(items))
	}
	if items[0] != "123" {
		t.Fatalf("expected first item %q, got %v", "123", items[0])
	}
	if items[1] != 7 {
		t.Fatalf("expected second item 7, got %v", items[1])
	}
	lastObj, ok := items[2].(map[string]interface{})
	if !ok {
		t.Fatalf("expected third item map, got %T", items[2])
	}
	if lastObj["desc"] != "uvw" {
		t.Fatalf("expected desc %q, got %v", "uvw", lastObj["desc"])
	}
}

func TestTruncateRawFunction_RawSliceMode_String(t *testing.T) {
	fn, ok := customFunctions["truncateRaw"]
	if !ok {
		t.Fatal("truncateRaw function not found")
	}

	got, err := fn("abcdefghijklmnopqrstuvwxyz", 0, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abcde" {
		t.Fatalf("expected %q, got %v", "abcde", got)
	}
}

func TestTruncateRawFunction_RawSliceMode_ObjectStringRepresentation(t *testing.T) {
	fn, ok := customFunctions["truncateRaw"]
	if !ok {
		t.Fatal("truncateRaw function not found")
	}

	got, err := fn(map[string]interface{}{"a": "1234567890"}, 0, 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if len([]rune(gotStr)) != 8 {
		t.Fatalf("expected length 8, got %d with value %q", len([]rune(gotStr)), gotStr)
	}
}

func TestTruncateRawFunction_TwoArgMode_FromZero(t *testing.T) {
	fn, ok := customFunctions["truncateRaw"]
	if !ok {
		t.Fatal("truncateRaw function not found")
	}

	got, err := fn("abcdefghij", 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abcd" {
		t.Fatalf("expected %q, got %v", "abcd", got)
	}
}

func TestTruncateRawFunction_TableEdgeCases(t *testing.T) {
	fn, ok := customFunctions["truncateRaw"]
	if !ok {
		t.Fatal("truncateRaw function not found")
	}

	tests := []struct {
		name string
		args []interface{}
		want interface{}
	}{
		{
			name: "negative start clamps to zero",
			args: []interface{}{"abcdef", -5, 3},
			want: "abc",
		},
		{
			name: "end beyond length clamps to end",
			args: []interface{}{"abcdef", 2, 100},
			want: "cdef",
		},
		{
			name: "start equals end returns empty",
			args: []interface{}{"abcdef", 3, 3},
			want: "",
		},
		{
			name: "start greater than end returns empty",
			args: []interface{}{"abcdef", 5, 2},
			want: "",
		},
		{
			name: "multibyte rune-safe slice",
			args: []interface{}{"a你b好c", 1, 4},
			want: "你b好",
		},
		{
			name: "deterministic map via json",
			args: []interface{}{map[string]interface{}{"b": "2", "a": "1"}, 0, 100},
			want: "{\"a\":\"1\",\"b\":\"2\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fn(tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestJsonPartFunction_WhenHTMLPresent(t *testing.T) {
	fn, ok := customFunctions["jsonPart"]
	if !ok {
		t.Fatal("jsonPart function not found")
	}

	input := `{"statusCode":200,"body":{"ok":true}}<html><body>PDF</body></html>`
	got, err := fn(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	want := `{"statusCode":200,"body":{"ok":true}}`
	if gotStr != want {
		t.Fatalf("expected %q, got %q", want, gotStr)
	}
}

func TestHtmlPartFunction_WhenHTMLPresent(t *testing.T) {
	fn, ok := customFunctions["htmlPart"]
	if !ok {
		t.Fatal("htmlPart function not found")
	}

	input := `{"statusCode":200}<html><body>Report</body></html>`
	got, err := fn(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	want := `<html><body>Report</body></html>`
	if gotStr != want {
		t.Fatalf("expected %q, got %q", want, gotStr)
	}
}

func TestDelimetterSwapFunction_DefaultKnownDelimitersToSemicolon(t *testing.T) {
	fn, ok := customFunctions["delimetterSwap"]
	if !ok {
		t.Fatal("delimetterSwap function not found")
	}

	got, err := fn(" , FCU Queue - PAN Aadhar not linked , FCU Queue_YOB Mismatch ; Other | Last ", ";")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	want := "FCU Queue - PAN Aadhar not linked;FCU Queue_YOB Mismatch;Other;Last"
	if gotStr != want {
		t.Fatalf("expected %q, got %q", want, gotStr)
	}
}

func TestDelimetterSwapFunction_SelectiveSourceDelimiter(t *testing.T) {
	fn, ok := customFunctions["delimetterSwap"]
	if !ok {
		t.Fatal("delimetterSwap function not found")
	}

	got, err := fn("A ; B ; C", "|", ";")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	want := "A|B|C"
	if gotStr != want {
		t.Fatalf("expected %q, got %q", want, gotStr)
	}
}

func TestDelimetterSwapFunction_EdgeCases(t *testing.T) {
	fn, ok := customFunctions["delimetterSwap"]
	if !ok {
		t.Fatal("delimetterSwap function not found")
	}

	tests := []struct {
		name string
		args []interface{}
		want string
	}{
		{
			name: "empty input returns empty",
			args: []interface{}{"", ";"},
			want: "",
		},
		{
			name: "spaces only input returns empty",
			args: []interface{}{"   ", ";"},
			want: "",
		},
		{
			name: "repeated default delimiters and empties are cleaned",
			args: []interface{}{"A,, ; ;|B||| C", ";"},
			want: "A;B;C",
		},
		{
			name: "output delimiter defaults to semicolon when empty",
			args: []interface{}{"A,B,C", ""},
			want: "A;B;C",
		},
		{
			name: "selective source delimiter no-match keeps trimmed value",
			args: []interface{}{"A,B,C", ";", "|"},
			want: "A,B,C",
		},
		{
			name: "single value remains unchanged except trim",
			args: []interface{}{"  FCU Queue  ", ";"},
			want: "FCU Queue",
		},
		{
			name: "two-arg call with nil output delimiter falls back",
			args: []interface{}{"A,B,C", nil},
			want: "A;B;C",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fn(tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotStr, ok := got.(string)
			if !ok {
				t.Fatalf("expected string result, got %T", got)
			}
			if gotStr != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, gotStr)
			}
		})
	}
}

func TestJsonAndHtmlPartFunctions_WhenNoHTMLPresent(t *testing.T) {
	jsonFn, ok := customFunctions["jsonPart"]
	if !ok {
		t.Fatal("jsonPart function not found")
	}
	htmlFn, ok := customFunctions["htmlPart"]
	if !ok {
		t.Fatal("htmlPart function not found")
	}

	input := `{"statusCode":200,"message":"ok"}`
	jsonGot, err := jsonFn(input)
	if err != nil {
		t.Fatalf("unexpected jsonPart error: %v", err)
	}
	htmlGot, err := htmlFn(input)
	if err != nil {
		t.Fatalf("unexpected htmlPart error: %v", err)
	}

	if jsonGot.(string) != input {
		t.Fatalf("expected jsonPart %q, got %q", input, jsonGot.(string))
	}
	if htmlGot.(string) != "" {
		t.Fatalf("expected empty htmlPart, got %q", htmlGot.(string))
	}
}

func TestBuildS3ObjectKey_EmptyPathUsesDefaultFileName(t *testing.T) {
	got := buildS3ObjectKey("", ".html")
	if got != "file.html" {
		t.Fatalf("expected %q, got %q", "file.html", got)
	}

	got = buildS3ObjectKey("", "html")
	if got != "file.html" {
		t.Fatalf("expected %q, got %q", "file.html", got)
	}

	got = buildS3ObjectKey("", "")
	if got != "" {
		t.Fatalf("expected empty key when both path and extension are empty, got %q", got)
	}
}

func TestUploadToS3WithConfig_RejectsEmptyContent(t *testing.T) {
	got, err := uploadToS3WithConfig("", ".html", "Crif_PDF/test", "bucket_name", "ap-south-1")
	if err == nil {
		t.Fatal("expected error for empty upload content")
	}
	if got != "" {
		t.Fatalf("expected empty URL for rejected upload, got %q", got)
	}
}
