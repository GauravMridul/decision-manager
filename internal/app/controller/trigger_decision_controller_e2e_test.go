package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"decision-manager/internal/app/constants"
	"decision-manager/internal/app/dto/request_dto/decision_manager_request_dto"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/internal/app/service/trigger_decision_service"
	"decision-manager/internal/app/utility"

	"github.com/dmi-infotech/common-modules/go/contracts"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type controllerTestLogger struct{}

func (l controllerTestLogger) Debug(msg string)                                          {}
func (l controllerTestLogger) Debugw(msg string, keysAndValues ...interface{})           {}
func (l controllerTestLogger) Debugf(format string, args ...interface{})                 {}
func (l controllerTestLogger) Info(args ...interface{})                                  {}
func (l controllerTestLogger) Infow(msg string, keysAndValues ...interface{})            {}
func (l controllerTestLogger) Infof(format string, args ...interface{})                  {}
func (l controllerTestLogger) Warn(msg string)                                           {}
func (l controllerTestLogger) Warnw(msg string, keysAndValues ...interface{})            {}
func (l controllerTestLogger) Warnf(format string, args ...interface{})                  {}
func (l controllerTestLogger) Error(msg string)                                          {}
func (l controllerTestLogger) Errorw(msg string, keysAndValues ...interface{})           {}
func (l controllerTestLogger) Errorf(format string, args ...interface{})                 {}
func (l controllerTestLogger) Fatal(msg string)                                          {}
func (l controllerTestLogger) Fatalf(format string, args ...interface{})                 {}
func (l controllerTestLogger) WithContext(ctx context.Context) contracts.Logger          { return l }
func (l controllerTestLogger) WithFields(fields map[string]interface{}) contracts.Logger { return l }

type controllerTestConfig struct {
	values map[string]interface{}
}

func (c *controllerTestConfig) GetString(key string) string {
	if v, ok := c.values[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
func (c *controllerTestConfig) GetInt(key string) int {
	if v, ok := c.values[key]; ok {
		if n, ok := v.(int); ok {
			return n
		}
	}
	return 0
}
func (c *controllerTestConfig) GetBool(key string) bool {
	if v, ok := c.values[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}
func (c *controllerTestConfig) GetFloat64(key string) float64 {
	if v, ok := c.values[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}
func (c *controllerTestConfig) GetStringSlice(key string) []string {
	if v, ok := c.values[key]; ok {
		if s, ok := v.([]string); ok {
			return s
		}
	}
	return nil
}
func (c *controllerTestConfig) IsSet(key string) bool                       { _, ok := c.values[key]; return ok }
func (c *controllerTestConfig) SetDefault(key string, value interface{})    { if !c.IsSet(key) { c.values[key] = value } }
func (c *controllerTestConfig) GetAll() map[string]interface{}              { return c.values }

type controllerTestService struct {
	triggerFn func(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error)
}

func (s *controllerTestService) TriggerDecision(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error) {
	return s.triggerFn(ctx, request, headers)
}

var _ trigger_decision_service.ITriggerDecisionService = (*controllerTestService)(nil)

func setupTriggerDecisionE2E(t *testing.T, svc trigger_decision_service.ITriggerDecisionService) *gin.Engine {
	t.Helper()

	previousModules := commoninit.GlobalModules
	t.Cleanup(func() {
		commoninit.GlobalModules = previousModules
	})

	commoninit.GlobalModules = &commoninit.CommonModules{
		Logger: controllerTestLogger{},
		Config: &controllerTestConfig{
			values: map[string]interface{}{
				constants.ApiKey:                            "test-api-key",
				constants.ServerMaxConcurrentTriggerDecisions: 1,
			},
		},
	}
	commoninit.InitTriggerDecisionLimiter()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	ctrl := NewTriggerDecisionController(validator.New(), utility.NewRequestValidator(), nil, svc)
	engine.POST("/trigger-decision", ctrl.TriggerDecision())
	return engine
}

func validTriggerDecisionPayload() map[string]interface{} {
	return map[string]interface{}{
		"Application_Id__c": "APP123",
		"LeadSource__c":     "Samsung",
		"RecordId__c":       "00QOW00000gv3cC2AQ",
		"StageEvent__c":     "Kyc",
		"customerId__c":     "CUST1",
		"workflowId__c":     "wf-1",
	}
}

func makeTriggerDecisionRequest(t *testing.T, payload map[string]interface{}, apiKey string, processMode string) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/trigger-decision", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(constants.CorrelationId, "corr-123")
	if apiKey != "" {
		req.Header.Set(constants.ApiKey, apiKey)
	}
	if processMode != "" {
		req.Header.Set(constants.ProcessModeHeaderKey, processMode)
	}
	return req
}

func TestTriggerDecisionE2E_UnauthorizedAPIKey(t *testing.T) {
	engine := setupTriggerDecisionE2E(t, &controllerTestService{
		triggerFn: func(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error) {
			t.Fatal("service should not be called on unauthorized request")
			return nil, nil
		},
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeTriggerDecisionRequest(t, validTriggerDecisionPayload(), "wrong-key", "sync"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTriggerDecisionE2E_BadPayloadValidation(t *testing.T) {
	engine := setupTriggerDecisionE2E(t, &controllerTestService{
		triggerFn: func(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error) {
			t.Fatal("service should not be called on bad payload")
			return nil, nil
		},
	})

	payload := validTriggerDecisionPayload()
	delete(payload, "RecordId__c")

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeTriggerDecisionRequest(t, payload, "test-api-key", "sync"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTriggerDecisionE2E_SyncSuccessResponse(t *testing.T) {
	engine := setupTriggerDecisionE2E(t, &controllerTestService{
		triggerFn: func(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error) {
			return map[string]map[string]interface{}{}, nil
		},
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeTriggerDecisionRequest(t, validTriggerDecisionPayload(), "test-api-key", "sync"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if body["status"] != "completed" {
		t.Fatalf("expected status=completed, got %v", body["status"])
	}
}

func TestTriggerDecisionE2E_SyncServiceError(t *testing.T) {
	engine := setupTriggerDecisionE2E(t, &controllerTestService{
		triggerFn: func(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error) {
			return nil, errors.New("sync failure")
		},
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeTriggerDecisionRequest(t, validTriggerDecisionPayload(), "test-api-key", "sync"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTriggerDecisionE2E_AsyncAcceptedAndBackgroundCompletes(t *testing.T) {
	callCh := make(chan struct{}, 1)
	engine := setupTriggerDecisionE2E(t, &controllerTestService{
		triggerFn: func(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error) {
			callCh <- struct{}{}
			return map[string]map[string]interface{}{}, nil
		},
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeTriggerDecisionRequest(t, validTriggerDecisionPayload(), "test-api-key", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if body["status"] != "processing" {
		t.Fatalf("expected status=processing, got %v", body["status"])
	}

	select {
	case <-callCh:
	case <-time.After(2 * time.Second):
		t.Fatal("background trigger service was not invoked")
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := commoninit.WaitForTrackedGoroutines(waitCtx); err != nil {
		t.Fatalf("background goroutines did not drain: %v", err)
	}
}

func TestTriggerDecisionE2E_OverloadReturnsRetryAfter(t *testing.T) {
	engine := setupTriggerDecisionE2E(t, &controllerTestService{
		triggerFn: func(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error) {
			t.Fatal("service should not be called when overloaded")
			return nil, nil
		},
	})

	// Occupy the single limiter slot configured in setup.
	if !commoninit.TryAcquireTriggerDecision() {
		t.Fatal("failed to acquire limiter slot for overload setup")
	}
	defer commoninit.ReleaseTriggerDecision()

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeTriggerDecisionRequest(t, validTriggerDecisionPayload(), "test-api-key", "sync"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "10" {
		t.Fatalf("expected Retry-After=10, got %q", got)
	}
}
