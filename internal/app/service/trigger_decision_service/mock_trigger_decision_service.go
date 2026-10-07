package trigger_decision_service

import (
	"context"
	"decision-manager/internal/app/dto/request_dto/decision_manager_request_dto"
	"encoding/json"
)

type MockTriggerDecisionService struct{}

func NewMockTriggerDecisionService() *MockTriggerDecisionService {
	return &MockTriggerDecisionService{}
}

func (m *MockTriggerDecisionService) TriggerDecision(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (string, error) {
	// Mock response structure
	mockResponse := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"applicationId": request.ApplicationID,
			"customerId":    request.CustomerID,
			"leadId":        request.LeadID,
			"workflowId":    request.WorkflowID,
			"decision": map[string]interface{}{
				"status":    "APPROVED",
				"score":     85,
				"timestamp": "2024-03-02T12:00:00Z",
				"rules": []map[string]interface{}{
					{
						"ruleId": "RULE_001",
						"name":   "Credit Score Check",
						"result": "PASS",
					},
					{
						"ruleId": "RULE_002",
						"name":   "Income Verification",
						"result": "PASS",
					},
				},
			},
			"metadata": map[string]interface{}{
				"partnerName": request.PartnerName,
				"programType": request.ProgramType,
				"stage":       request.Stage,
				"processedAt": "2024-03-02T12:00:00Z",
			},
		},
	}

	// Convert the mock response to JSON string
	responseJSON, err := json.Marshal(mockResponse)
	if err != nil {
		return "", err
	}

	return string(responseJSON), nil
}
