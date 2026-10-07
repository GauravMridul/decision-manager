package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"decision-manager/internal/app/constants"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/internal/app/service/cache_service"

	"github.com/gin-gonic/gin"
)

type cacheControllerTestService struct {
	clearFn func(ctx context.Context, request cache_service.ClearCacheRequest) (*cache_service.ClearCacheResult, error)
}

func (s *cacheControllerTestService) ClearCache(ctx context.Context, request cache_service.ClearCacheRequest) (*cache_service.ClearCacheResult, error) {
	return s.clearFn(ctx, request)
}

func (s *cacheControllerTestService) PingCache(ctx context.Context) error {
	return nil
}

func (s *cacheControllerTestService) GetCacheStats(ctx context.Context) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (s *cacheControllerTestService) GetValueByKey(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (s *cacheControllerTestService) Close(ctx context.Context) error {
	return nil
}

func setupCacheControllerTest(t *testing.T, values map[string]interface{}, svc cache_service.ICacheService) *gin.Engine {
	t.Helper()

	previousModules := commoninit.GlobalModules
	t.Cleanup(func() {
		commoninit.GlobalModules = previousModules
	})

	commoninit.GlobalModules = &commoninit.CommonModules{
		Logger: controllerTestLogger{},
		Config: &controllerTestConfig{
			values: values,
		},
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	ctrl := NewCacheController(svc)
	engine.POST("/cache/clear", ctrl.ClearCache)
	return engine
}

func makeCacheClearRequest(t *testing.T, payload map[string]interface{}, adminKey string) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/cache/clear", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if adminKey != "" {
		req.Header.Set(constants.ApiKey, adminKey)
	}
	return req
}

func TestCacheController_InvalidAPIKey(t *testing.T) {
	engine := setupCacheControllerTest(t, map[string]interface{}{
		constants.ApiKey: "admin-key",
	}, &cacheControllerTestService{
		clearFn: func(ctx context.Context, request cache_service.ClearCacheRequest) (*cache_service.ClearCacheResult, error) {
			t.Fatal("service should not be called on invalid api key")
			return nil, nil
		},
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeCacheClearRequest(t, map[string]interface{}{"clear_all": true}, "wrong-key"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCacheController_InvalidRequest(t *testing.T) {
	engine := setupCacheControllerTest(t, map[string]interface{}{
		constants.ApiKey: "admin-key",
	}, &cacheControllerTestService{
		clearFn: func(ctx context.Context, request cache_service.ClearCacheRequest) (*cache_service.ClearCacheResult, error) {
			t.Fatal("service should not be called on invalid request")
			return nil, nil
		},
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeCacheClearRequest(t, map[string]interface{}{
		"clear_all": true,
		"match":     "decision-manager",
	}, "admin-key"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCacheController_DryRunMatchSuccess(t *testing.T) {
	engine := setupCacheControllerTest(t, map[string]interface{}{
		constants.ApiKey: "admin-key",
	}, &cacheControllerTestService{
		clearFn: func(ctx context.Context, request cache_service.ClearCacheRequest) (*cache_service.ClearCacheResult, error) {
			if request.ClearAll {
				t.Fatalf("expected clear_all=false, got true")
			}
			if request.Match != "decision-manager" {
				t.Fatalf("unexpected match: %s", request.Match)
			}
			if !request.DryRun {
				t.Fatalf("expected dry_run=true")
			}
			if request.LimitMatchKey != 1 {
				t.Fatalf("expected limitMatchKey=1 got %d", request.LimitMatchKey)
			}
			return &cache_service.ClearCacheResult{
				Mode:        "match",
				Pattern:     "*decision-manager*",
				DryRun:      true,
				KeysMatched: 2,
				KeysDeleted: 0,
				ScanCycles:  1,
				Keys:        []string{"decision-managerdevserviceSfdcFieldMapping:13", "decision-managerdevpartnerServiceMapping:abc"},
			}, nil
		},
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeCacheClearRequest(t, map[string]interface{}{
		"match":         "decision-manager",
		"dry_run":       true,
		"limitMatchKey": 1,
	}, "admin-key"))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCacheController_InvalidLimitMatchKey(t *testing.T) {
	engine := setupCacheControllerTest(t, map[string]interface{}{
		constants.ApiKey: "admin-key",
	}, &cacheControllerTestService{
		clearFn: func(ctx context.Context, request cache_service.ClearCacheRequest) (*cache_service.ClearCacheResult, error) {
			t.Fatal("service should not be called on invalid limitMatchKey")
			return nil, nil
		},
	})

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, makeCacheClearRequest(t, map[string]interface{}{
		"match":         "decision-manager",
		"dry_run":       true,
		"limitMatchKey": -1,
	}, "admin-key"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}
