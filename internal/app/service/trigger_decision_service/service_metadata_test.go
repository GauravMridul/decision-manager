package trigger_decision_service

import "testing"

func TestIsEmptyServiceResponse(t *testing.T) {
	tests := []struct {
		name        string
		serviceData map[string]interface{}
		want        bool
	}{
		{
			name:        "missing_response_is_not_empty",
			serviceData: map[string]interface{}{"serviceName": "A"},
			want:        false,
		},
		{
			name: "response_body_nil_is_empty",
			serviceData: map[string]interface{}{
				"serviceName": "A",
				"response":    map[string]interface{}{"body": nil},
			},
			want: true,
		},
		{
			name: "response_body_empty_map_is_empty",
			serviceData: map[string]interface{}{
				"serviceName": "A",
				"response":    map[string]interface{}{"body": map[string]interface{}{}},
			},
			want: true,
		},
		{
			name: "response_body_non_empty_map_is_not_empty",
			serviceData: map[string]interface{}{
				"serviceName": "A",
				"response":    map[string]interface{}{"body": map[string]interface{}{"ok": true}},
			},
			want: false,
		},
		{
			name: "response_body_empty_list_is_empty",
			serviceData: map[string]interface{}{
				"serviceName": "A",
				"response":    map[string]interface{}{"body": []interface{}{}},
			},
			want: true,
		},
		{
			name: "response_body_non_empty_list_is_not_empty",
			serviceData: map[string]interface{}{
				"serviceName": "A",
				"response":    map[string]interface{}{"body": []interface{}{1}},
			},
			want: false,
		},
		{
			name: "response_body_blank_string_is_empty",
			serviceData: map[string]interface{}{
				"serviceName": "A",
				"response":    map[string]interface{}{"body": "   "},
			},
			want: true,
		},
		{
			name: "response_body_non_blank_string_is_not_empty",
			serviceData: map[string]interface{}{
				"serviceName": "A",
				"response":    map[string]interface{}{"body": "x"},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isEmptyServiceResponse(tc.serviceData)
			if got != tc.want {
				t.Fatalf("isEmptyServiceResponse()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractServiceMetadata_FiltersAndBuildsMaps(t *testing.T) {
	valueJSON := map[string]interface{}{
		"CIBIL": map[string]interface{}{
			"serviceName": "CIBIL",
			"serviceId":   float64(1),
			"response": map[string]interface{}{
				"body": map[string]interface{}{"score": 760},
			},
		},
		"MobileUANService": map[string]interface{}{
			"serviceName": "MobileUANService",
			"serviceId":   "113",
			"response": map[string]interface{}{
				"body": map[string]interface{}{}, // empty body => should be filtered out
			},
		},
		"NoServiceName": map[string]interface{}{
			"serviceId": float64(10),
			"response": map[string]interface{}{
				"body": map[string]interface{}{"ok": true},
			},
		},
		"InvalidServiceID": map[string]interface{}{
			"serviceName": "InvalidServiceID",
			"serviceId":   "not-a-number",
			"response": map[string]interface{}{
				"body": map[string]interface{}{"ok": true},
			},
		},
		"BodyMissingButHasServiceName": map[string]interface{}{
			"serviceName": "BodyMissingButHasServiceName",
			"serviceId":   float64(55),
			"response":    map[string]interface{}{}, // body missing => not considered empty by current logic
		},
	}

	serviceNameArray, allowedServices, serviceIDToName := extractServiceMetadata(valueJSON)

	if len(serviceNameArray) != 3 {
		t.Fatalf("expected 3 services in serviceNameArray, got %d (%v)", len(serviceNameArray), serviceNameArray)
	}
	if _, ok := allowedServices["CIBIL"]; !ok {
		t.Fatalf("expected CIBIL in allowedServices")
	}
	if _, ok := allowedServices["MobileUANService"]; ok {
		t.Fatalf("did not expect MobileUANService in allowedServices (empty body)")
	}
	if _, ok := allowedServices["NoServiceName"]; ok {
		t.Fatalf("did not expect NoServiceName in allowedServices (missing serviceName field)")
	}

	if got, ok := serviceIDToName[1]; !ok || got != "CIBIL" {
		t.Fatalf("expected serviceIDToName[1]=CIBIL, got %q", got)
	}
	if _, ok := serviceIDToName[113]; ok {
		t.Fatalf("did not expect serviceIDToName[113] from MobileUANService (empty body)")
	}
	if _, ok := serviceIDToName[55]; !ok {
		t.Fatalf("expected serviceIDToName[55] from BodyMissingButHasServiceName")
	}
}

