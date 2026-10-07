package utility

import "testing"

func TestArrayToString_ExpandedPrimitiveArgs_JoinsAllValues(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn(
		"BUREAU SCORE NORMS NOT MET",
		"A SCORE NORMS NOT MET - STPL V6",
		"BANKING AA NORMS NOT MET",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}

	want := "BUREAU SCORE NORMS NOT MET,A SCORE NORMS NOT MET - STPL V6,BANKING AA NORMS NOT MET"
	if gotStr != want {
		t.Fatalf("expected %q, got %q", want, gotStr)
	}
}

func TestArrayToString_SliceString_JoinsWithComma(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "a,b,c" {
		t.Fatalf("expected %q, got %q", "a,b,c", gotStr)
	}
}

func TestArrayToString_SliceObject_WithFieldExtraction(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn(
		[]interface{}{
			map[string]interface{}{"uan": "111"},
			map[string]interface{}{"uan": "222"},
		},
		"uan",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "111,222" {
		t.Fatalf("expected %q, got %q", "111,222", gotStr)
	}
}

func TestArrayToString_SingleMap_WithFieldExtraction(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn(map[string]interface{}{"uan": "123456"}, "uan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "123456" {
		t.Fatalf("expected %q, got %q", "123456", gotStr)
	}
}

func TestArrayToString_SingleMap_WithEmptyExtractedField(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn(map[string]interface{}{"uan": ""}, "uan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "" {
		t.Fatalf("expected empty string, got %q", gotStr)
	}
}

func TestArrayToString_ScalarWithFieldName_MissingPathStyle_ReturnsScalarOnly(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn("", "uan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "" {
		t.Fatalf("expected empty string, got %q", gotStr)
	}
}

func TestEvaluateExpression_ArrayToString_KarzaAndMobileFormats_BothNonEmpty(t *testing.T) {
	valueJSON := map[string]interface{}{
		"KarzaMobileToUAN": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"result": map[string]interface{}{
						"uan": []interface{}{"100000000001", "100000000002"},
					},
				},
			},
		},
		"MobileUANService": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"data": map[string]interface{}{
						"result": []interface{}{
							map[string]interface{}{"uan": "100000000004"},
						},
					},
				},
			},
		},
	}

	expr := "{{(arrayToString(MobileUANService.response.body.data.result,'uan')!=''||arrayToString(KarzaMobileToUAN.response.body.result.uan)!='')}}"
	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	match, ok := got.(bool)
	if !ok {
		t.Fatalf("expected bool result, got %T", got)
	}
	if !match {
		t.Fatalf("expected true for non-empty UAN data from both service formats")
	}
}

func TestArrayToString_EmptyArgs_ReturnsEmpty(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "" {
		t.Fatalf("expected empty string, got %q", gotStr)
	}
}

func TestArrayToString_SinglePrimitive_ReturnsAsString(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn(627)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "627" {
		t.Fatalf("expected %q, got %q", "627", gotStr)
	}
}

func TestEvaluateExpression_ArrayToString_PostBureauRejectReasons_JoinsAllValues(t *testing.T) {
	valueJSON := map[string]interface{}{
		"PostBureauFinnable": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"Reject_reason": []interface{}{
						"LOAN AMOUNT NORMS NOT MET",
						"IXSIGHT NEGATIVE LIST MATCH FOUND",
					},
				},
			},
		},
	}

	expr := "{{arrayToString(((PostBureauFinnable.response.body.Reject_reason)))}}"
	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	want := "LOAN AMOUNT NORMS NOT MET,IXSIGHT NEGATIVE LIST MATCH FOUND"
	if gotStr != want {
		t.Fatalf("expected %q, got %q", want, gotStr)
	}
}

func TestEvaluateExpression_ArrayToString_TwoPrimitiveValues_SecondLooksLikeFieldName(t *testing.T) {
	valueJSON := map[string]interface{}{
		"SVC": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"Reject_reason": []interface{}{
						"FIRST_REASON",
						"SECOND_REASON",
					},
				},
			},
		},
	}

	expr := "{{arrayToString(((SVC.response.body.Reject_reason)))}}"
	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	want := "FIRST_REASON,SECOND_REASON"
	if gotStr != want {
		t.Fatalf("expected %q, got %q", want, gotStr)
	}
}

func TestArrayToString_ExpandedObjectArgs_WithTrailingFieldName(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn(
		map[string]interface{}{"uan": "111"},
		map[string]interface{}{"uan": "222"},
		"uan",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "111,222" {
		t.Fatalf("expected %q, got %q", "111,222", gotStr)
	}
}

func TestArrayToString_TwoPrimitiveArgs_NoFalseFieldExtraction(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	got, err := fn("A", "uan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "A,uan" {
		t.Fatalf("expected %q, got %q", "A,uan", gotStr)
	}
}

func TestArrayToString_MultiValue_TableDriven(t *testing.T) {
	fn, ok := customFunctions["arrayToString"]
	if !ok {
		t.Fatal("arrayToString function not found")
	}

	tests := []struct {
		name string
		args []interface{}
		want string
	}{
		{
			name: "slice interface strings",
			args: []interface{}{[]interface{}{"R1", "R2", "R3"}},
			want: "R1,R2,R3",
		},
		{
			name: "slice interface numbers",
			args: []interface{}{[]interface{}{1, 2, 3}},
			want: "1,2,3",
		},
		{
			name: "expanded primitive values",
			args: []interface{}{"R1", "R2", "R3"},
			want: "R1,R2,R3",
		},
		{
			name: "expanded primitive second looks field name",
			args: []interface{}{"R1", "uan", "R3"},
			want: "R1,uan,R3",
		},
		{
			name: "object slice field extraction all found",
			args: []interface{}{
				[]interface{}{
					map[string]interface{}{"reason": "A"},
					map[string]interface{}{"reason": "B"},
					map[string]interface{}{"reason": "C"},
				},
				"reason",
			},
			want: "A,B,C",
		},
		{
			name: "object slice field extraction missing value skipped",
			args: []interface{}{
				[]interface{}{
					map[string]interface{}{"reason": "A"},
					map[string]interface{}{"other": "X"},
					map[string]interface{}{"reason": "C"},
				},
				"reason",
			},
			want: "A,C",
		},
		{
			name: "expanded object args with trailing field name",
			args: []interface{}{
				map[string]interface{}{"reason": "A"},
				map[string]interface{}{"reason": "B"},
				map[string]interface{}{"reason": "C"},
				"reason",
			},
			want: "A,B,C",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fn(tc.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotStr, ok := got.(string)
			if !ok {
				t.Fatalf("expected string result, got %T", got)
			}
			if gotStr != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, gotStr)
			}
		})
	}
}

func TestEvaluateExpression_ArrayToString_SecondLiteralLooksLikeFieldName(t *testing.T) {
	valueJSON := map[string]interface{}{
		"SVC": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"Reject_reason": []interface{}{"FIRST", "uan", "THIRD"},
				},
			},
		},
	}

	expr := "{{arrayToString(((SVC.response.body.Reject_reason)))}}"
	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}
	if gotStr != "FIRST,uan,THIRD" {
		t.Fatalf("expected %q, got %q", "FIRST,uan,THIRD", gotStr)
	}
}
