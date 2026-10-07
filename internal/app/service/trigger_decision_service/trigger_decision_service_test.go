package trigger_decision_service

import (
	"context"
	"decision-manager/internal/app/dto/request_dto/decision_manager_request_dto"
	"encoding/json"
	"testing"
)

func TestMockTriggerDecisionService(t *testing.T) {
	// Create a new instance of the mock service
	mockService := NewMockTriggerDecisionService()

	// Create a sample request
	request := &decision_manager_request_dto.TriggerDecisionRequest{
		ApplicationID: "APP123",
		CustomerID:    "CUST456",
		LeadID:        "LEAD789",
		WorkflowID:    "WF001",
		PartnerName:   "TestPartner",
		ProgramType:   "TestProgram",
		Stage:         "TestStage",
	}

	// Create mock headers
	headers := map[string]string{
		"X-Correlation-ID": "test-correlation-id",
	}

	// Call the mock service
	response, err := mockService.TriggerDecision(context.Background(), request, headers)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Verify the response is valid JSON
	var responseMap map[string]interface{}
	err = json.Unmarshal([]byte(response), &responseMap)
	if err != nil {
		t.Errorf("Expected valid JSON response, got error: %v", err)
	}

	// Verify the response contains expected fields
	if status, ok := responseMap["status"].(string); !ok || status != "success" {
		t.Error("Expected status 'success' in response")
	}

	// Verify data exists in response
	data, ok := responseMap["data"].(map[string]interface{})
	if !ok {
		t.Error("Expected data object in response")
		return
	}

	// Verify request fields are reflected in response
	if appID, ok := data["applicationId"].(string); !ok || appID != request.ApplicationID {
		t.Errorf("Expected applicationId %s, got %v", request.ApplicationID, appID)
	}

	// Verify decision object exists
	decision, ok := data["decision"].(map[string]interface{})
	if !ok {
		t.Error("Expected decision object in response")
		return
	}

	// Verify decision status
	if status, ok := decision["status"].(string); !ok || status != "APPROVED" {
		t.Errorf("Expected decision status 'APPROVED', got %v", status)
	}
}
