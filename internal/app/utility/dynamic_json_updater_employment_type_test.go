package utility

import (
	"encoding/json"
	"testing"
)

func TestEvaluateExpression_StandardEmploymentType_WithProvidedResponseShape(t *testing.T) {
	valueJSON := map[string]interface{}{
		"KarzaMobileToUAN": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{},
			},
			"status": "SKIPPED",
		},
		"MobileUANService": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"data": map[string]interface{}{
						"result": []interface{}{
							map[string]interface{}{"uan": ""},
						},
					},
				},
			},
			"status": "COMPLETED",
		},
		"contact": map[string]interface{}{
			"EmploymentType__c": "",
		},
		"lead": map[string]interface{}{
			"Sourcing_Program__c": nil,
		},
		"CIBIL": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"consumerCreditData": []interface{}{
						map[string]interface{}{
							"accounts": []interface{}{
								map[string]interface{}{"accountType": "69"},
								map[string]interface{}{"accountType": "05"},
								map[string]interface{}{"accountType": "02"},
								map[string]interface{}{"accountType": "10"},
							},
						},
					},
				},
			},
		},
	}

	expr := "{{((((serializeJson(KarzaMobileToUAN.response.body)!='null'&&serializeJson(KarzaMobileToUAN.response.body)!='{}'&&concat(KarzaMobileToUAN.response.body)!='')||toLower(KarzaMobileToUAN.status)=='skipped')||((serializeJson(MobileUANService.response.body)!='null'&&serializeJson(MobileUANService.response.body)!='{}'&&concat(MobileUANService.response.body)!='')||toLower(MobileUANService.status)=='skipped'))?(inList(toLower(concat(contact.EmploymentType__c)),'self employed','self employed professional','senp','sep')&&contact.EmploymentType__c!='')?'Self Employed':(inListField(CIBIL.response.body.consumerCreditData[0].accounts,'accountType','12','14','23','24','33','38','39','40','50','51','52','53','54','55','56','57','58','59','61','71')?'Self Employed':(inList(toLower(concat(lead.Sourcing_Program__c)),'offline store','qro2o')&&contact.EmploymentType__c!=''&&toLower(concat(contact.EmploymentType__c))=='salaried')?'Salaried':(arrayToString(MobileUANService.response.body.data.result,'uan')!=''||arrayToString(KarzaMobileToUAN.response.body.result.uan)!='')?'Salaried':'Data not Found'):null)}}"

	got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", got)
	}

	if gotStr != "Data not Found" {
		t.Fatalf("expected %q, got %q", "Data not Found", gotStr)
	}
}

func TestEvaluateExpression_StandardEmploymentType_CRIFIntegerScenarios(t *testing.T) {
	expr := "{{(((serializeJson(KarzaMobileToUAN.response.body)!='null'&&serializeJson(KarzaMobileToUAN.response.body)!='{}'&&concat(KarzaMobileToUAN.response.body)!='')||toLower(KarzaMobileToUAN.status)=='skipped')||((serializeJson(MobileUANService.response.body)!='null'&&serializeJson(MobileUANService.response.body)!='{}'&&concat(MobileUANService.response.body)!='')||toLower(MobileUANService.status)=='skipped'))?((inList(toLower(concat(contact.EmploymentType__c)),'self employed','self employed professional','senp','sep')&&contact.EmploymentType__c!='')?'Self Employed':(((concat(acticoData.response.body.body.crif.features.NUM_BUSINESS_LOANS)!='')&&(acticoData.response.body.body.crif.features.NUM_BUSINESS_LOANS>0))?'Self Employed':((inList(toLower(concat(lead.Sourcing_Program__c)),'offline store','qro2o')&&contact.EmploymentType__c!=''&&toLower(concat(contact.EmploymentType__c))=='salaried')?'Salaried':((((serializeJson(MobileUANService.response.body)!='null'&&serializeJson(MobileUANService.response.body)!='{}'&&concat(MobileUANService.response.body)!='')&&arrayToString(MobileUANService.response.body.data.result,'uan')!='')||((serializeJson(KarzaMobileToUAN.response.body)!='null'&&serializeJson(KarzaMobileToUAN.response.body)!='{}'&&concat(KarzaMobileToUAN.response.body)!='')&&arrayToString(KarzaMobileToUAN.response.body.result.uan)!=''))?'Salaried':'Data Not Fetched'))):null)}}"

	tests := []struct {
		name     string
		mutate   func(map[string]interface{})
		expected interface{}
	}{
		{
			name: "returns nil when top-level gate is false",
			mutate: func(v map[string]interface{}) {
				v["KarzaMobileToUAN"] = map[string]interface{}{
					"response": map[string]interface{}{
						"body": map[string]interface{}{},
					},
					"status": "COMPLETED",
				}
				v["MobileUANService"] = map[string]interface{}{
					"response": map[string]interface{}{
						"body": map[string]interface{}{},
					},
					"status": "COMPLETED",
				}
			},
			expected: nil,
		},
		{
			name: "contact self employed wins",
			mutate: func(v map[string]interface{}) {
				v["contact"] = map[string]interface{}{
					"EmploymentType__c": "self employed",
				}
				setNumBusinessLoans(v, 0)
			},
			expected: "Self Employed",
		},
		{
			name: "num business loans integer greater than zero marks self employed",
			mutate: func(v map[string]interface{}) {
				setNumBusinessLoans(v, 3)
			},
			expected: "Self Employed",
		},
		{
			name: "num business loans zero with offline salaried returns salaried",
			mutate: func(v map[string]interface{}) {
				setNumBusinessLoans(v, 0)
				v["lead"] = map[string]interface{}{
					"Sourcing_Program__c": "offline store",
				}
				v["contact"] = map[string]interface{}{
					"EmploymentType__c": "salaried",
				}
			},
			expected: "Salaried",
		},
		{
			name: "num business loans zero with mobile uan present returns salaried",
			mutate: func(v map[string]interface{}) {
				setNumBusinessLoans(v, 0)
				setMobileUAN(v, "100200300400")
			},
			expected: "Salaried",
		},
		{
			name: "num business loans zero with karza uan present returns salaried",
			mutate: func(v map[string]interface{}) {
				setNumBusinessLoans(v, 0)
				setKarzaUAN(v, "445566778899")
			},
			expected: "Salaried",
		},
		{
			name: "num business loans zero without salaried or uan data returns data not fetched",
			mutate: func(v map[string]interface{}) {
				setNumBusinessLoans(v, 0)
			},
			expected: "Data Not Fetched",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valueJSON := baseCRIFExpressionPayload()
			tt.mutate(valueJSON)

			got, err := evaluateExpression(expr, valueJSON, false, testLogger{}, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Fatalf("expected %v (%T), got %v (%T)", tt.expected, tt.expected, got, got)
			}
		})
	}
}

func baseCRIFExpressionPayload() map[string]interface{} {
	payload := map[string]interface{}{
		"KarzaMobileToUAN": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{},
			},
			"status": "SKIPPED",
		},
		"MobileUANService": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"data": map[string]interface{}{
						"result": []interface{}{
							map[string]interface{}{"uan": ""},
						},
					},
				},
			},
			"status": "COMPLETED",
		},
		"contact": map[string]interface{}{
			"EmploymentType__c": "",
		},
		"lead": map[string]interface{}{
			"Sourcing_Program__c": "",
		},
		"acticoData": map[string]interface{}{
			"response": map[string]interface{}{
				"body": map[string]interface{}{
					"body": map[string]interface{}{
						"crif": map[string]interface{}{
							"features": map[string]interface{}{
								"NUM_BUSINESS_LOANS": 0,
							},
						},
					},
				},
			},
		},
	}
	return deepCopyMap(payload)
}

func setNumBusinessLoans(v map[string]interface{}, n int) {
	actico := v["acticoData"].(map[string]interface{})
	response := actico["response"].(map[string]interface{})
	body := response["body"].(map[string]interface{})
	innerBody := body["body"].(map[string]interface{})
	crif := innerBody["crif"].(map[string]interface{})
	features := crif["features"].(map[string]interface{})
	features["NUM_BUSINESS_LOANS"] = n
}

func setMobileUAN(v map[string]interface{}, uan string) {
	mobile := v["MobileUANService"].(map[string]interface{})
	response := mobile["response"].(map[string]interface{})
	body := response["body"].(map[string]interface{})
	data := body["data"].(map[string]interface{})
	data["result"] = []interface{}{
		map[string]interface{}{"uan": uan},
	}
}

func setKarzaUAN(v map[string]interface{}, uan string) {
	karza := v["KarzaMobileToUAN"].(map[string]interface{})
	response := karza["response"].(map[string]interface{})
	response["body"] = map[string]interface{}{
		"result": map[string]interface{}{
			"uan": []interface{}{uan},
		},
	}
}

func deepCopyMap(src map[string]interface{}) map[string]interface{} {
	raw, err := json.Marshal(src)
	if err != nil {
		panic(err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}
