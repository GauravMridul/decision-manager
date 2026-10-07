package trigger_decision_service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"decision-manager/internal/app/constants"

	"decision-manager/internal/app/db/repository"

	"decision-manager/internal/app/dto/request_dto/decision_manager_request_dto"
	"decision-manager/internal/app/dto/request_dto/external_service_adapter_request_dto"

	"decision-manager/internal/app/models/decision_manager_log_models"
	"decision-manager/internal/app/models/decision_manager_models"
	"decision-manager/internal/app/utility"

	"decision-manager/pkg/client"

	commoninit "decision-manager/internal/app/init"

	"github.com/dmi-infotech/common-modules/go/contracts"

	// "strconv"
	"time"
)

type ITriggerDecisionService interface {
	TriggerDecision(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error)
}

type TriggerDecisionService struct {
	DynamicJsonUpdater                *utility.DynamicJsonUpdater
	ServiceSfdcFieldMappingRepository repository.IServiceSfdcFieldMappingRepository
	SalesforceClient                  client.ISalesforceClient
	PartnerServiceMappingRepository   repository.IPartnerServiceMappingInterface
	// ApiClient1                         client.IApiClient
	DecisionManagerLogRepository repository.IDecisionManagerLogRepository
	RedisClient                  contracts.CacheProvider
	RedisKeyGenerator            *utility.RedisKeyGenerator
	logger                       contracts.Logger
	ApiClient                    contracts.APIClient
	partnerServiceMappingTTL     time.Duration
	serviceSfdcFieldMappingTTL   time.Duration

	asyncDMLogInitOnce     sync.Once
	asyncDMLogCloseOnce    sync.Once
	telemetryInitOnce      sync.Once
	asyncDMLogQueue        chan asyncDecisionManagerLogTask
	asyncDMLogTimeout      time.Duration
	asyncDMLogShuttingDown atomic.Uint32 // 0 = running, 1 = shutting down
	asyncDMLogWg           sync.WaitGroup

	asyncDMLogEnqueued            atomic.Int64
	asyncDMLogDequeued            atomic.Int64
	asyncDMLogWorkerInsertSuccess atomic.Int64
	asyncDMLogWorkerInsertFailure atomic.Int64
	asyncDMLogInlineFallback      atomic.Int64
	asyncDMLogInlineFailure       atomic.Int64
}

func NewTriggerDecisionService(dynamicJsonUpdater *utility.DynamicJsonUpdater, serviceSfdcFieldMappingRepository repository.IServiceSfdcFieldMappingRepository, salesforceClient client.ISalesforceClient, partnerServiceMappingRepository repository.IPartnerServiceMappingInterface, apiClient contracts.APIClient, decisionManagerLogRepository repository.IDecisionManagerLogRepository, redisKeyGenerator *utility.RedisKeyGenerator) *TriggerDecisionService {
	partnerServiceMappingTTL, serviceSfdcFieldMappingTTL, partnerFromConfig, serviceSfdcFromConfig := resolveDecisionManagerCacheTTLs()
	svc := &TriggerDecisionService{
		DynamicJsonUpdater:                dynamicJsonUpdater,
		ServiceSfdcFieldMappingRepository: serviceSfdcFieldMappingRepository,
		SalesforceClient:                  salesforceClient,
		PartnerServiceMappingRepository:   partnerServiceMappingRepository,
		DecisionManagerLogRepository:      decisionManagerLogRepository,
		RedisClient:                       commoninit.GetCache(),
		RedisKeyGenerator:                 redisKeyGenerator,
		logger:                            commoninit.GetLogger(),
		ApiClient:                         apiClient,
		partnerServiceMappingTTL:          partnerServiceMappingTTL,
		serviceSfdcFieldMappingTTL:        serviceSfdcFieldMappingTTL,
	}
	if svc.logger != nil {
		svc.logger.Infow("Decision Manager cache TTLs resolved",
			"partner_service_mapping_ttl", partnerServiceMappingTTL.String(),
			"partner_service_mapping_ttl_source", ttlSourceLabel(partnerFromConfig),
			"service_sfdc_field_mapping_ttl", serviceSfdcFieldMappingTTL.String(),
			"service_sfdc_field_mapping_ttl_source", ttlSourceLabel(serviceSfdcFromConfig),
			"fallback_ttl", defaultCacheTTLFallback.String())
	}
	svc.initAsyncDMLogWriter()
	svc.initRuntimeTelemetry()
	return svc
}

type asyncDecisionManagerLogTask struct {
	ctx           context.Context
	requestLogger contracts.Logger
	logEntry      *decision_manager_log_models.DecisionManagerLog
	customerID    string
}

const (
	defaultCacheTTLFallback         = 24 * time.Hour
	asyncDMLogInlineFallbackTimeout = time.Duration(constants.DefaultAsyncDMLogInlineFallbackMs) * time.Millisecond
)

func ttlSourceLabel(fromConfig bool) string {
	if fromConfig {
		return "config"
	}
	return "fallback"
}

func resolveDecisionManagerCacheTTLs() (time.Duration, time.Duration, bool, bool) {
	partnerTTL := defaultCacheTTLFallback
	serviceSfdcTTL := defaultCacheTTLFallback
	partnerFromConfig := false
	serviceSfdcFromConfig := false

	if commoninit.IsDecisionManagerCacheConfigAvailable() {
		cacheConfig := commoninit.GetDecisionManagerCacheConfig()
		if cacheConfig.PartnerServiceMappingTTL > 0 {
			partnerTTL = cacheConfig.PartnerServiceMappingTTL
			partnerFromConfig = true
		}
		if cacheConfig.ServiceSFDCFieldMappingTTL > 0 {
			serviceSfdcTTL = cacheConfig.ServiceSFDCFieldMappingTTL
			serviceSfdcFromConfig = true
		}
	}

	return partnerTTL, serviceSfdcTTL, partnerFromConfig, serviceSfdcFromConfig
}

func (s *TriggerDecisionService) initAsyncDMLogWriter() {
	s.asyncDMLogInitOnce.Do(func() {
		workerCount := commoninit.GetConfigInt(constants.AuditLogAsyncDMWorkersKey, constants.DefaultAsyncDMLogWorkers)
		if workerCount < 1 {
			workerCount = constants.DefaultAsyncDMLogWorkers
		}
		queueSize := commoninit.GetConfigInt(constants.AuditLogAsyncDMQueueSizeKey, constants.DefaultAsyncDMLogQueueSize)
		if queueSize < 1 {
			queueSize = constants.DefaultAsyncDMLogQueueSize
		}
		timeoutSeconds := commoninit.GetConfigInt(constants.AuditLogAsyncDMTimeoutSecondsKey, constants.DefaultAsyncDMLogTimeoutSeconds)
		if timeoutSeconds < 1 {
			timeoutSeconds = constants.DefaultAsyncDMLogTimeoutSeconds
		}

		s.asyncDMLogQueue = make(chan asyncDecisionManagerLogTask, queueSize)
		s.asyncDMLogTimeout = time.Duration(timeoutSeconds) * time.Second

		for workerID := 1; workerID <= workerCount; workerID++ {
			s.asyncDMLogWg.Add(1)
			go s.runAsyncDMLogWorker(workerID)
		}

		if s.logger != nil {
			s.logger.Infow("Initialized async decision-manager log writer", "workers", workerCount, "queue_size", queueSize, "timeout_seconds", timeoutSeconds)
		}
	})
}

func (s *TriggerDecisionService) runAsyncDMLogWorker(workerID int) {
	defer s.asyncDMLogWg.Done()
	for task := range s.asyncDMLogQueue {
		s.asyncDMLogDequeued.Add(1)
		baseCtx := task.ctx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		log := task.requestLogger
		if log == nil {
			log = s.logger.WithContext(baseCtx)
		}

		insertStart := time.Now()
		insertCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), s.asyncDMLogTimeout)
		err := s.DecisionManagerLogRepository.InsertLog(insertCtx, task.logEntry)
		cancel()
		durationMs := time.Since(insertStart).Milliseconds()

		if err != nil {
			s.asyncDMLogWorkerInsertFailure.Add(1)
			log.Errorw("Failed to save Decision Manager Log (async worker)", "error", err, "decisionManagerLog", task.logEntry, "stage_duration_ms", durationMs, "worker_id", workerID)
			log.Infow("Checkpoint", "checkpoint", "mongo_log_completed", "stage_duration_ms", durationMs, "status", "failed", "customerId", task.customerID, "worker_id", workerID)
			continue
		}

		s.asyncDMLogWorkerInsertSuccess.Add(1)
		log.Infow("Checkpoint", "checkpoint", "mongo_log_completed", "stage_duration_ms", durationMs, "status", "success", "customerId", task.customerID, "worker_id", workerID)
	}
}

const (
	asyncDMLogShutdownDrainTimeout = 8 * time.Second
	asyncDMLogShutdownWaitTimeout  = 10 * time.Second
)

// Close gracefully shuts down the async DM log queue: stops new enqueues (they
// fall back to inline insert), drains remaining tasks via inline insert, closes
// the channel so workers exit, then waits for workers to finish.
// Matches the ESA pattern: no audit log is silently dropped.
func (s *TriggerDecisionService) Close(ctx context.Context) error {
	s.asyncDMLogCloseOnce.Do(func() {
		if s.asyncDMLogQueue == nil {
			return
		}
		s.asyncDMLogShuttingDown.Store(1)

		drainCtx, drainCancel := context.WithTimeout(ctx, asyncDMLogShutdownDrainTimeout)
		var drained []asyncDecisionManagerLogTask
	drainLoop:
		for {
			select {
			case task, ok := <-s.asyncDMLogQueue:
				if !ok {
					drainCancel()
					break drainLoop
				}
				drained = append(drained, task)
			case <-drainCtx.Done():
				drainCancel()
				break drainLoop
			default:
				drainCancel()
				break drainLoop
			}
		}

		if len(drained) > 0 && s.logger != nil {
			s.logger.Infow("Shutdown: drained async DM log queue; persisting inline",
				"drained_task_count", len(drained))
		}
		for _, task := range drained {
			s.insertDMLogInline(task.ctx, task.requestLogger, task.logEntry, task.customerID)
		}

		close(s.asyncDMLogQueue)
		s.asyncDMLogQueue = nil

		done := make(chan struct{})
		go func() {
			s.asyncDMLogWg.Wait()
			close(done)
		}()
		// Do not tie worker wait to parent ctx: after draining + many inline inserts,
		// modules shutdown ctx may already be expired; we still need up to 10s for
		// workers to finish before Mongo disconnect runs.
		waitCtx, waitCancel := context.WithTimeout(context.Background(), asyncDMLogShutdownWaitTimeout)
		defer waitCancel()
		select {
		case <-done:
			if s.logger != nil {
				s.logger.Info("Async DM log queue closed; all workers exited")
			}
		case <-waitCtx.Done():
			if s.logger != nil {
				s.logger.Errorw("Async DM log workers did not exit within timeout; shutdown proceeding",
					"timeout_seconds", int(asyncDMLogShutdownWaitTimeout.Seconds()))
			}
		}
	})
	return nil
}

func (s *TriggerDecisionService) enqueueDecisionManagerLog(ctx context.Context, requestLogger contracts.Logger, dml *decision_manager_log_models.DecisionManagerLog, customerID string) {
	if dml == nil || s.DecisionManagerLogRepository == nil {
		return
	}
	s.initAsyncDMLogWriter()
	if s.asyncDMLogQueue == nil {
		return
	}

	if s.asyncDMLogShuttingDown.Load() != 0 {
		s.insertDMLogInline(ctx, requestLogger, dml, customerID)
		return
	}

	task := asyncDecisionManagerLogTask{
		ctx:           ctx,
		requestLogger: requestLogger,
		logEntry:      dml,
		customerID:    customerID,
	}
	select {
	case s.asyncDMLogQueue <- task:
		s.asyncDMLogEnqueued.Add(1)
	default:
		s.asyncDMLogInlineFallback.Add(1)
		log := requestLogger
		if log == nil {
			log = s.logger.WithContext(ctx)
		}
		log.Warnw("Async decision-manager log queue full; trying inline fallback insert", "customerId", customerID)
		s.insertDMLogInline(ctx, requestLogger, dml, customerID)
	}
}

func (s *TriggerDecisionService) insertDMLogInline(ctx context.Context, requestLogger contracts.Logger, dml *decision_manager_log_models.DecisionManagerLog, customerID string) {
	if s.DecisionManagerLogRepository == nil || dml == nil {
		return
	}
	log := requestLogger
	if log == nil {
		log = s.logger.WithContext(ctx)
	}
	baseCtx := ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	insertStart := time.Now()
	insertCtx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), asyncDMLogInlineFallbackTimeout)
	err := s.DecisionManagerLogRepository.InsertLog(insertCtx, dml)
	cancel()
	durationMs := time.Since(insertStart).Milliseconds()
	if err != nil {
		s.asyncDMLogInlineFailure.Add(1)
		log.Errorw("Failed to save Decision Manager Log (inline fallback)", "error", err, "stage_duration_ms", durationMs, "customerId", customerID)
		return
	}
	log.Infow("Checkpoint", "checkpoint", "mongo_log_completed", "stage_duration_ms", durationMs, "status", "success_inline_fallback", "customerId", customerID)
}

func (s *TriggerDecisionService) initRuntimeTelemetry() {
	s.telemetryInitOnce.Do(func() {
		if !commoninit.GetConfigBool(constants.TelemetryEnabledKey, false) {
			return
		}

		intervalSeconds := commoninit.GetConfigInt(constants.TelemetryIntervalSecondsKey, constants.DefaultTelemetryIntervalSeconds)
		if intervalSeconds < 10 {
			intervalSeconds = 10
		}
		memstatsEnabled := commoninit.GetConfigBool(constants.TelemetryMemstatsEnabledKey, true)
		cacheSampleEnabled := commoninit.GetConfigBool(constants.TelemetryCacheSampleEnabledKey, false)
		cacheSampleIntervalSeconds := commoninit.GetConfigInt(constants.TelemetryCacheSampleIntervalSecondsKey, constants.DefaultTelemetryCacheSampleIntervalSeconds)
		if cacheSampleIntervalSeconds < intervalSeconds {
			cacheSampleIntervalSeconds = intervalSeconds
		}
		cacheSampleMaxEntries := commoninit.GetConfigInt(constants.TelemetryCacheSampleMaxEntriesKey, constants.DefaultTelemetryCacheSampleMaxEntries)
		if cacheSampleMaxEntries < 1 {
			cacheSampleMaxEntries = constants.DefaultTelemetryCacheSampleMaxEntries
		}

		interval := time.Duration(intervalSeconds) * time.Second
		cacheSampleInterval := time.Duration(cacheSampleIntervalSeconds) * time.Second

		commoninit.StartTrackedGoroutine(func() {
			shutdownSignal := commoninit.GetAppShutdownSignal()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			cacheSampleTicker := time.NewTicker(cacheSampleInterval)
			defer cacheSampleTicker.Stop()

			for {
				select {
				case <-shutdownSignal:
					if s.logger != nil {
						s.logger.Info("Stopping runtime telemetry loop due to shutdown signal")
					}
					return
				case <-ticker.C:
					s.logRuntimeTelemetrySnapshot(memstatsEnabled)
				case <-cacheSampleTicker.C:
					if cacheSampleEnabled {
						s.logRuntimeCacheSample(cacheSampleMaxEntries)
					}
				}
			}
		})

		if s.logger != nil {
			s.logger.Infow("Runtime telemetry enabled",
				"interval_seconds", intervalSeconds,
				"memstats_enabled", memstatsEnabled,
				"cache_sample_enabled", cacheSampleEnabled,
				"cache_sample_interval_seconds", cacheSampleIntervalSeconds,
				"cache_sample_max_entries", cacheSampleMaxEntries)
		}
	})
}

func (s *TriggerDecisionService) logRuntimeTelemetrySnapshot(memstatsEnabled bool) {
	if s.logger == nil {
		return
	}

	cacheStats := utility.GetDynamicJSONCacheStats()
	queueLen := len(s.asyncDMLogQueue)
	queueCap := cap(s.asyncDMLogQueue)

	fields := []interface{}{
		"telemetry", "runtime_snapshot",
		"async_dm_queue_len", queueLen,
		"async_dm_queue_cap", queueCap,
		"async_dm_log_enqueued_total", s.asyncDMLogEnqueued.Load(),
		"async_dm_log_dequeued_total", s.asyncDMLogDequeued.Load(),
		"async_dm_worker_insert_success_total", s.asyncDMLogWorkerInsertSuccess.Load(),
		"async_dm_worker_insert_failure_total", s.asyncDMLogWorkerInsertFailure.Load(),
		"async_dm_inline_fallback_total", s.asyncDMLogInlineFallback.Load(),
		"async_dm_inline_failure_total", s.asyncDMLogInlineFailure.Load(),
		"dynamic_json_expr_cache_entries", cacheStats.ExprCacheEntries,
		"dynamic_json_expr_cache_max_entries", cacheStats.ExprCacheMaxEntries,
		"go_goroutines", runtime.NumGoroutine(),
	}

	if memstatsEnabled {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		fields = append(fields,
			"go_heap_alloc_mb", bytesToMB(mem.HeapAlloc),
			"go_heap_inuse_mb", bytesToMB(mem.HeapInuse),
			"go_heap_idle_mb", bytesToMB(mem.HeapIdle),
			"go_heap_released_mb", bytesToMB(mem.HeapReleased),
			"go_stack_inuse_mb", bytesToMB(mem.StackInuse),
			"go_sys_mb", bytesToMB(mem.Sys),
			"go_num_gc", mem.NumGC)
	}

	s.logger.Infow("Runtime telemetry snapshot", fields...)
}

func (s *TriggerDecisionService) logRuntimeCacheSample(maxEntries int) {
	if s.logger == nil {
		return
	}
	cacheSample := utility.SampleDynamicJSONCacheStats(maxEntries)
	s.logger.Infow("Runtime cache sample",
		"telemetry", "runtime_cache_sample",
		"sample_max_entries", maxEntries,
		"expr_samples", cacheSample.ExprSamples,
		"expr_avg_key_bytes", cacheSample.AvgExprKeyBytes)
}

func bytesToMB(v uint64) float64 {
	return float64(v) / (1024 * 1024)
}

var stageAliasMap = map[string]string{
	"ready_for_decision": "loan-decision",
	"kyc":                "kyc-decision",
	"kyc-decision":       "kyc-decision",
	// add more stage aliases here as needed
}

func resolveStage(stage string) string {
	if stage == "" {
		return stage
	}

	normalized := strings.ToLower(stage)
	if resolved, ok := stageAliasMap[normalized]; ok {
		return resolved
	}

	return stage
}

func (s *TriggerDecisionService) TriggerDecision(ctx context.Context, request *decision_manager_request_dto.TriggerDecisionRequest, headers map[string]string) (map[string]map[string]interface{}, error) {
	methodName := "TriggerDecision: "
	log := s.logger.WithContext(ctx)
	if reqLog := utility.GetRequestLogger(ctx); reqLog != nil {
		log = reqLog
	}
	requestStartTime := utility.GetRequestStartTime(ctx)
	triggerDecisionStartTime := time.Now()
	if requestStartTime.IsZero() {
		requestStartTime = triggerDecisionStartTime
	}

	originalStage := request.Stage
	request.Stage = resolveStage(request.Stage)
	if originalStage != request.Stage {
		log.Infow("Stage resolved", "originalStage", originalStage, "resolvedStage", request.Stage)
	}

	log.Info("Inside Trigger decision service method for partner: ", request.PartnerName, " programType: ", request.ProgramType, " businessType: ", request.BusinessType, " sourcingProgram: ", request.SourcingProgram, " loanCategory: ", request.LoanCategory, " customerType: ", request.CustomerType, " productLine: ", request.ProductLine, " salesChannelPartnerName: ", request.SalesChannelPartnerName, " sourcingChannel: ", request.SourcingChannel, " nameOfConsolidator: ", request.NameOfConsolidator, " stage: ", request.Stage)

	var partnerServiceMapping *decision_manager_models.PartnerServiceMappingResponse
	var shouldFetchFromDB bool

	if s.RedisClient == nil {
		log.Info("Failed to connect with redis client. ")
		return nil, errors.New("failed to connect with redis client")
	}

	redisKey := s.RedisKeyGenerator.GenerateRedisKeyForPartnerServiceMapping(request.PartnerName, request.ProgramType, request.BusinessType, request.SourcingProgram, request.LoanCategory, request.CustomerType, request.ProductLine, request.SalesChannelPartnerName, request.SourcingChannel, request.NameOfConsolidator, request.Stage)
	cachedPartnerServiceMapping, redisErr := s.RedisClient.Get(ctx, redisKey)

	// Check if we need to fetch from database
	if redisErr != nil {
		log.Info("Failed to fetch partner service mapping from redis cache, will fetch from database  redisKey: ", redisKey, " error: ", redisErr)
		shouldFetchFromDB = true
	} else if len(cachedPartnerServiceMapping) > 0 {
		// Try to unmarshal cached data
		if err := json.Unmarshal([]byte(cachedPartnerServiceMapping), &partnerServiceMapping); err != nil {
			log.Errorw("Failed to unmarshal cached partner service mapping, will fetch from database", "redisKey", redisKey, "error", err)
			shouldFetchFromDB = true
		} else {
			log.Info("Partner service mapping fetched from redis cache redisKey: ", redisKey, " partnerServiceMapping: ", partnerServiceMapping)
		}
	} else {
		log.Info("Empty cached data, will fetch from database redisKey: ", redisKey)
		shouldFetchFromDB = true
	}

	// Fetch from database if needed
	if shouldFetchFromDB {
		var err error
		partnerServiceMapping, err = s.PartnerServiceMappingRepository.FindByExcludingIdAndName(ctx, request.PartnerName, request.ProgramType, request.BusinessType, request.SourcingProgram, request.LoanCategory, request.CustomerType, request.ProductLine, request.SalesChannelPartnerName, request.SourcingChannel, request.NameOfConsolidator, request.Stage)
		if err != nil {
			log.Errorw("Error fetching partner service mapping from database", "error", err)
			return nil, err
		}
		log.Info("Partner service mapping fetched from database partnerServiceMapping: ", partnerServiceMapping)

		//Do not set redis key when there is no entry for given sequenceArray.
		if partnerServiceMapping == nil {
			return nil, errors.New("no partner service mapping found")
		}

		response, err := json.Marshal(partnerServiceMapping)
		if err != nil {
			log.Errorw("Failed to marshal partner service mapping for Redis", "method", methodName, "error", err)
		} else {
			err := s.RedisClient.Set(ctx, redisKey, response, s.partnerServiceMappingTTL)
			if err != nil {
				log.Errorw("Failed to set partner service mapping in Redis", "method", methodName, "redisKey", redisKey, "error", err)
			} else {
				log.Info("Set redis key successfull for key: ", redisKey)
			}
		}
	}

	metadataElapsed := time.Since(requestStartTime).Milliseconds()
	metadataDuration := time.Since(triggerDecisionStartTime).Milliseconds()
	log.Infow("Checkpoint", "checkpoint", "metadata_fetched", "elapsed_since_start_ms", metadataElapsed, "stage_duration_ms", metadataDuration)

	decisionManagerLog := &decision_manager_log_models.DecisionManagerLog{
		EsaRequestBody: &external_service_adapter_request_dto.ExternalServiceAdapterRequest{
			PartnerName:             request.PartnerName,
			ProgramType:             request.ProgramType,
			BusinessType:            request.BusinessType,
			SourcingProgram:         request.SourcingProgram,
			LoanCategory:            request.LoanCategory,
			CustomerType:            request.CustomerType,
			ProductLine:             request.ProductLine,
			SalesChannelPartnerName: request.SalesChannelPartnerName,
			SourcingChannel:         request.SourcingChannel,
			NameOfConsolidator:      request.NameOfConsolidator,
			Stage:                   request.Stage,
			ApplicationID:           request.ApplicationID,
			CustomerID:              request.LeadID,
			WorkflowID:              request.WorkflowID,
			OldRefID:                request.LeadID,
			SequenceID:              strconv.FormatInt(partnerServiceMapping.ID, 10),
			SequenceString:          partnerServiceMapping.ServiceSequenceString,
		},
	}

	dmiBaseUrl := commoninit.GetConfigString(constants.EsaBaseUrlKey)
	url := dmiBaseUrl + constants.ExternalServiceAdapter
	headers["X-Api-Key"] = commoninit.GetConfigString(constants.EsaApiKeyKey)

	log.Infow("ESA request body",
		"esaRequestBody", decisionManagerLog.EsaRequestBody,
		"headers", headers,
		"url", url,
		"method", constants.POST)

	httpRequest, err := s.ApiClient.CreateJSONRequestWithHeaders(ctx, constants.POST, url, headers, decisionManagerLog.EsaRequestBody)
	if err != nil {
		return nil, err
	}

	esaExecutionStartTime := time.Now()
	log.Info("ESA execution started executionStartTime: ", esaExecutionStartTime)

	timeoutSeconds := commoninit.GetConfigInt("RestExecuteTimeoutInSeconds", 30)
	//maxConnectionRetries default is 1
	maxConnectionRetries := commoninit.GetConfigInt(constants.EsaConnectionRetryCountKey, constants.DefaultEsaConnectionRetryCount)
	statusCode, responseString, err := s.ApiClient.RestExecuteWithConnectionRetry(ctx, httpRequest, timeoutSeconds, maxConnectionRetries)
	if err != nil {
		return nil, err
	}

	esaExecutionEndTime := time.Now()
	esaExecutionTime := esaExecutionEndTime.Sub(esaExecutionStartTime).Milliseconds()
	log.Info("ESA execution ended executionEndTime: ", esaExecutionEndTime)
	log.Info("ESA response received statusCode: ", statusCode, " esaExecutionTime: ", esaExecutionTime)

	if statusCode != 200 {
		if statusCode == http.StatusGatewayTimeout {
			s.handleESAProcessSequenceTimeout(ctx, log, request, decisionManagerLog, triggerDecisionStartTime, requestStartTime, esaExecutionStartTime, esaExecutionEndTime, esaExecutionTime, responseString)
		}
		return nil, errors.New("failed to execute esa request, error: Non success response. statusCode: " + strconv.Itoa(statusCode))
	}
	// (Removed ~1,740 lines of commented-out hardcoded ESA response JSON that
	// contained PII. Test fixtures belong in _test.go files or testdata/.)

	decisionManagerLog.EsaResponseBody = responseString
	esaDoneElapsed := time.Since(requestStartTime).Milliseconds()
	log.Infow("Checkpoint", "checkpoint", "esa_invocation_done", "elapsed_since_start_ms", esaDoneElapsed, "stage_duration_ms", esaExecutionTime)

	if err := ctx.Err(); err != nil {
		log.Warnw("Context cancelled after ESA invocation, aborting", "error", err)
		return nil, err
	}

	// Transform ESA JSON response to the required format
	transformStart := time.Now()
	valueJson, err := utility.TransformESAResponse(responseString)
	if err != nil {
		return nil, err
	}
	transformDuration := time.Since(transformStart).Milliseconds()
	// log.Info("Transformed ESA response: ", valueJson)
	log.Info("ESA response transformed successfully")
	esaTransformedElapsed := time.Since(requestStartTime).Milliseconds()
	log.Infow("Checkpoint", "checkpoint", "esa_response_transformed", "elapsed_since_start_ms", esaTransformedElapsed, "stage_duration_ms", transformDuration)

	if err := ctx.Err(); err != nil {
		log.Warnw("Context cancelled after ESA transform, aborting", "error", err)
		return nil, err
	}

	sfdcMappingStart := time.Now()
	serviceSfdcFieldMappingRequestBodyJson, err := s.serviceSfdcFieldMappingFetch(ctx, valueJson, partnerServiceMapping)
	if err != nil {
		return nil, err
	}
	sfdcMappingDuration := time.Since(sfdcMappingStart).Milliseconds()
	sfdcMappingElapsed := time.Since(requestStartTime).Milliseconds()
	log.Infow("Checkpoint", "checkpoint", "sfdc_field_mapping_fetched", "elapsed_since_start_ms", sfdcMappingElapsed, "stage_duration_ms", sfdcMappingDuration)

	if err := ctx.Err(); err != nil {
		log.Warnw("Context cancelled after SFDC field mapping fetch, aborting", "error", err)
		return nil, err
	}

	// Preprocessing pass: run BEFORE any arrayPath resolution or body
	// interpolation. Produces "_pp*" top-level keys in valueJson (e.g.
	// parsed CRIF JSON tree) that downstream templates and arrayPath
	// references can consume. Original ESA data remains untouched.
	//
	// Failure isolation: ApplyPreprocessings logs and nil-stores any rule
	// that fails (bad expr, custom-func error, cycle, missing dep, ...) so
	// a single misconfigured rule never aborts the whole request. The only
	// error returned here is ctx.Err() if the client cancelled mid-flight.
	//
	// The returned summary (per-rule outcomes + produced "_pp*" values + total
	// duration) is persisted into the decision-manager mongo log below so the
	// preprocessing stage is fully auditable / replayable without grepping
	// application logs.
	preprocessStart := time.Now()
	preprocessRuleCount := 0
	for _, r := range serviceSfdcFieldMappingRequestBodyJson {
		preprocessRuleCount += len(r.PreProcessing)
	}
	preprocessingSummary, err := s.DynamicJsonUpdater.ApplyPreprocessings(ctx, valueJson, serviceSfdcFieldMappingRequestBodyJson)
	if err != nil {
		// Persist whatever summary we built before cancellation so the mongo
		// record still shows which rules ran (or didn't) up to the abort.
		if preprocessingSummary != nil {
			decisionManagerLog.SetField("preprocessing", preprocessingSummary)
			decisionManagerLog.SetField("preprocessingTimeMs", preprocessingSummary.DurationMs)
		}
		return nil, fmt.Errorf("preprocessing failed: %w", err)
	}
	preprocessDuration := time.Since(preprocessStart).Milliseconds()
	if preprocessingSummary != nil {
		// Persist the full summary (counters, per-rule outcomes, produced
		// "_pp*" values) and a top-level convenience field for time, so
		// dashboards can chart preprocessing latency without descending
		// into the nested object.
		decisionManagerLog.SetField("preprocessing", preprocessingSummary)
		decisionManagerLog.SetField("preprocessingTimeMs", preprocessingSummary.DurationMs)
	}
	log.Infow("Checkpoint", "checkpoint", "preprocessing_done",
		"elapsed_since_start_ms", time.Since(requestStartTime).Milliseconds(),
		"stage_duration_ms", preprocessDuration,
		"rawRuleCount", preprocessRuleCount)

	if err := ctx.Err(); err != nil {
		log.Warnw("Context cancelled after preprocessing, aborting", "error", err)
		return nil, err
	}

	compositeBuildStart := time.Now()
	ctx = utility.SetRequestLogger(ctx, log)
	sfCompositeSubRequestArray, err := s.DynamicJsonUpdater.InterpolateValuesWithArrayExpansion(ctx, serviceSfdcFieldMappingRequestBodyJson, valueJson)
	if err != nil {
		return nil, err
	}
	compositeBuildDuration := time.Since(compositeBuildStart).Milliseconds()
	compositeBuildElapsed := time.Since(requestStartTime).Milliseconds()
	log.Infow("Checkpoint", "checkpoint", "sf_composite_request_built", "elapsed_since_start_ms", compositeBuildElapsed, "stage_duration_ms", compositeBuildDuration)

	// Apply post-expansion merge logic for array-expanded items
	// sfCompositeSubRequestArray = s.applyPostExpansionMerge(ctx, sfCompositeSubRequestArray, serviceSfdcFieldMappingRequestBodyJson)

	// Debug: Log items after array expansion
	// for i, item := range sfCompositeSubRequestArray {
	// 	if strings.Contains(item.RefID, "Offer__c") && i < 10 { // Log first 10 Offer__c items
	// 		log.Info(fmt.Sprintf("After array expansion: item[%d]: refID=%s, url=%s",
	// 			i, item.RefID, item.URL))
	// 	}
	// }

	if err := ctx.Err(); err != nil {
		log.Warnw("Context cancelled after composite request build, aborting", "error", err)
		return nil, err
	}

	log.Info("SF Composite Request Array Size: ", len(sfCompositeSubRequestArray))
	decisionManagerLog.SFCompositeSubRequestArray = sfCompositeSubRequestArray
	sfCompositeExecutionStartTime := time.Now()
	log.Info("SF Composite execution started executionStartTime: ", sfCompositeExecutionStartTime)
	retryLog := &utility.SFRetryLog{}
	ctx = utility.SetSFRetryLog(ctx, retryLog)
	sfCompositeResponse, err := s.SalesforceClient.UpsertSalesforceObjects(ctx, sfCompositeSubRequestArray)
	if err != nil {
		return nil, err
	}
	if len(retryLog.Events) > 0 {
		decisionManagerLog.SetField("sfRetryEvents", retryLog.Events)
		decisionManagerLog.SetField("sfRetryCount", len(retryLog.Events))
	}
	log.Info("SF composite response: ", sfCompositeResponse)
	sfCompositeExecutionEndTime := time.Now()
	log.Info("SF Composite execution ended executionEndTime: ", sfCompositeExecutionEndTime)
	sfCompositeExecutionTime := sfCompositeExecutionEndTime.Sub(sfCompositeExecutionStartTime).Milliseconds()
	log.Info("SF Composite execution time sfCompositeExecutionTime: ", sfCompositeExecutionTime)
	sfCompositeElapsed := time.Since(requestStartTime).Milliseconds()
	log.Infow("Checkpoint", "checkpoint", "sf_composite_executed", "elapsed_since_start_ms", sfCompositeElapsed, "stage_duration_ms", sfCompositeExecutionTime)

	// Call Salesforce Apex REST callback after composite upsert and before exception logging
	// Extract lead ID from transformed ESA valueJson
	leadID := request.LeadID

	apexCallbackStartTime := time.Now()
	apexStatusCode := 0
	apexResponseBody := ""
	var apexExecErr error
	if len(leadID) > 0 {
		exceptionLoggerForApex := utility.NewSFExceptionLogger()
		apexStatusCode, apexResponseBody, apexExecErr = exceptionLoggerForApex.CallDecisionCallbackApex(ctx, leadID, request.Stage)
		if apexExecErr != nil {
			log.Errorw("Apex decision callback failed", "leadId", leadID, "statusCode", apexStatusCode, "error", apexExecErr)
		} else if apexStatusCode != 200 {
			log.Warnw("Apex decision callback returned non-200", "leadId", leadID, "statusCode", apexStatusCode, "response", apexResponseBody)
		} else {
			log.Infow("Apex decision callback executed", "leadId", leadID, "statusCode", apexStatusCode)
		}
	} else {
		log.Warn("Lead ID not found in transformed ESA response; skipping Apex decision callback")
	}
	apexCallbackEndTime := time.Now()
	apexCallbackTime := apexCallbackEndTime.Sub(apexCallbackStartTime).Milliseconds()
	callbackElapsed := time.Since(requestStartTime).Milliseconds()
	log.Infow("Checkpoint", "checkpoint", "callback_sent", "elapsed_since_start_ms", callbackElapsed, "stage_duration_ms", apexCallbackTime, "apex_status_code", apexStatusCode)

	// Ensure ExtraFields is initialized and persist Apex callback audit to Mongo log
	if decisionManagerLog.ExtraFields == nil {
		decisionManagerLog.ExtraFields = make(map[string]interface{})
	}
	decisionManagerLog.ExtraFields["apexCallbackRequest"] = map[string]interface{}{
		"leadId":       leadID,
		"callbackType": request.Stage,
	}
	decisionManagerLog.ExtraFields["apexCallbackStatusCode"] = apexStatusCode
	decisionManagerLog.ExtraFields["apexCallbackResponse"] = apexResponseBody
	decisionManagerLog.ExtraFields["apexCallbackStartTime"] = apexCallbackStartTime
	decisionManagerLog.ExtraFields["apexCallbackEndTime"] = apexCallbackEndTime
	decisionManagerLog.ExtraFields["apexCallbackTime"] = apexCallbackTime

	// Build a merged map for exception logging that includes Apex callback failure (non-200)
	mergedForException := make(map[string]map[string]interface{}, len(sfCompositeResponse)+1)
	for k, v := range sfCompositeResponse {
		mergedForException[k] = v
	}
	if apexExecErr != nil || apexStatusCode != 200 {
		mergedForException["ApexCallback"] = map[string]interface{}{
			"success":     false,
			"statusCode":  apexStatusCode,
			"message":     "Apex Decision callback failed",
			"referenceId": "ApexCallback",
			"body":        apexResponseBody,
		}
	}

	// Log SF exceptions asynchronously to avoid increasing decisioning time
	exceptionInitiatedElapsed := time.Since(requestStartTime).Milliseconds()
	log.Infow("Checkpoint", "checkpoint", "exception_log_initiated", "elapsed_since_start_ms", exceptionInitiatedElapsed)
	merged := mergedForException
	commoninit.StartTrackedGoroutine(func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorw("Recovered panic in async SF exception logger",
					"panic", r,
					"error", fmt.Sprintf("%v", r),
					"stackTrace", string(debug.Stack()))
			}
		}()
		exceptionLoggerStartTime := time.Now()
		exceptionLogger := utility.NewSFExceptionLogger()
		asyncExceptionCtx := context.WithoutCancel(ctx)
		if err := exceptionLogger.LogSFExceptions(asyncExceptionCtx, valueJson, merged); err != nil {
			log.Errorw("Failed to log SF exceptions asynchronously", "error", err)
		}
		exceptionLoggerEndTime := time.Now()
		exceptionLoggerTime := exceptionLoggerEndTime.Sub(exceptionLoggerStartTime).Milliseconds()
		log.Info("Async exception logger time exceptionLoggerTime: ", exceptionLoggerTime)
	})

	decisionManagerLog.SFCompositeResponse = sfCompositeResponse

	triggerDecisionEndTime := time.Now()
	triggerDecisionTime := triggerDecisionEndTime.Sub(triggerDecisionStartTime).Milliseconds()
	log.Info("Trigger decision time triggerDecisionTime: ", triggerDecisionTime)

	decisionManagerLog.TriggerDecisionStartTime = triggerDecisionStartTime
	decisionManagerLog.TriggerDecisionEndTime = triggerDecisionEndTime
	decisionManagerLog.TriggerDecisionTime = triggerDecisionTime
	decisionManagerLog.EsaExecutionStartTime = esaExecutionStartTime
	decisionManagerLog.EsaExecutionEndTime = esaExecutionEndTime
	decisionManagerLog.EsaExecutionTime = esaExecutionTime
	decisionManagerLog.SFCompositeExecutionStartTime = sfCompositeExecutionStartTime
	decisionManagerLog.SFCompositeExecutionEndTime = sfCompositeExecutionEndTime
	decisionManagerLog.SFCompositeExecutionTime = sfCompositeExecutionTime

	// Save execution logs to MongoDB (async by default; sync when audit.log.sync is true)
	if s.DecisionManagerLogRepository != nil {
		mongoInitiatedElapsed := time.Since(requestStartTime).Milliseconds()
		log.Infow("Checkpoint", "checkpoint", "mongo_log_initiated", "elapsed_since_start_ms", mongoInitiatedElapsed)
		auditLogSync := commoninit.GetConfigBool(constants.AuditLogSyncKey, false)
		if auditLogSync {
			if err := s.DecisionManagerLogRepository.InsertLog(ctx, decisionManagerLog); err != nil {
				log.Errorw("Failed to save Decision Manager Log (sync)", "error", err, "decisionManagerLog", decisionManagerLog)
			} else {
				log.Info("Successfully saved Decision Manager Log (sync) customerId: ", request.CustomerID)
			}
		} else {
			s.enqueueDecisionManagerLog(ctx, log, decisionManagerLog, request.CustomerID)
		}
	} else {
		log.Warn("DecisionManagerLogRepository is nil - cannot save decision manager logs")
	}

	triggerDecisionEndPostMongoInsertionTime := time.Now()
	triggerDecisionPostMongoInsertionTime := triggerDecisionEndPostMongoInsertionTime.Sub(triggerDecisionStartTime).Milliseconds()
	log.Info("Trigger decision end post mongo insertion time triggerDecisionEndPostMongoInsertionTime: ", triggerDecisionPostMongoInsertionTime)

	return sfCompositeResponse, nil
}

// handleESAProcessSequenceTimeout persists Salesforce Exception_Log__c and DecisionManagerLog when ESA returns
// HTTP 504 (process-sequence deadline). SF and Mongo are best-effort and independent of each other.
func (s *TriggerDecisionService) handleESAProcessSequenceTimeout(
	ctx context.Context,
	log contracts.Logger,
	request *decision_manager_request_dto.TriggerDecisionRequest,
	decisionManagerLog *decision_manager_log_models.DecisionManagerLog,
	triggerDecisionStartTime, requestStartTime time.Time,
	esaExecutionStartTime, esaExecutionEndTime time.Time,
	esaExecutionTime int64,
	responseString string,
) {
	if decisionManagerLog == nil || log == nil {
		return
	}

	decisionManagerLog.EsaResponseBody = responseString
	decisionManagerLog.EsaExecutionStartTime = esaExecutionStartTime
	decisionManagerLog.EsaExecutionEndTime = esaExecutionEndTime
	decisionManagerLog.EsaExecutionTime = esaExecutionTime

	triggerDecisionEndTime := time.Now()
	decisionManagerLog.TriggerDecisionStartTime = triggerDecisionStartTime
	decisionManagerLog.TriggerDecisionEndTime = triggerDecisionEndTime
	decisionManagerLog.TriggerDecisionTime = triggerDecisionEndTime.Sub(triggerDecisionStartTime).Milliseconds()

	if decisionManagerLog.ExtraFields == nil {
		decisionManagerLog.ExtraFields = make(map[string]interface{})
	}
	decisionManagerLog.ExtraFields["httpStatus"] = http.StatusGatewayTimeout
	decisionManagerLog.ExtraFields["errorType"] = "esa_process_sequence_timeout"

	var bodyObj map[string]interface{}
	if err := json.Unmarshal([]byte(responseString), &bodyObj); err == nil {
		if td, ok := bodyObj["timeout_diagnostics"]; ok {
			decisionManagerLog.ExtraFields["timeout_diagnostics"] = td
		}
	}

	log.Infow("Checkpoint", "checkpoint", "esa_process_sequence_timeout",
		"elapsed_since_start_ms", time.Since(requestStartTime).Milliseconds())

	// Salesforce exception log — independent of Mongo outcome
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorw("Recovered panic in ESA timeout Salesforce exception logger",
					"panic", r,
					"stackTrace", string(debug.Stack()))
			}
		}()
		if strings.TrimSpace(request.LeadID) == "" {
			log.Warn("Skipping Salesforce exception log for ESA 504: LeadID is empty")
			return
		}
		exceptionLogger := utility.NewSFExceptionLogger()
		sfCtx := context.WithoutCancel(ctx)
		if err := exceptionLogger.LogESAProcessSequenceTimeout(sfCtx, request.LeadID, responseString); err != nil {
			log.Errorw("Failed to log ESA process-sequence timeout to Salesforce", "error", err, "leadId", request.LeadID)
		}
	}()

	// Mongo DecisionManagerLog — same audit settings as success path; independent of SF outcome
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorw("Recovered panic persisting DecisionManagerLog after ESA 504",
					"panic", r,
					"stackTrace", string(debug.Stack()))
			}
		}()
		if s.DecisionManagerLogRepository == nil {
			log.Warn("DecisionManagerLogRepository is nil - cannot save decision manager logs after ESA 504")
			return
		}
		mongoInitiatedElapsed := time.Since(requestStartTime).Milliseconds()
		log.Infow("Checkpoint", "checkpoint", "mongo_log_initiated_after_esa_504", "elapsed_since_start_ms", mongoInitiatedElapsed)
		auditLogSync := commoninit.GetConfigBool(constants.AuditLogSyncKey, false)
		if auditLogSync {
			if err := s.DecisionManagerLogRepository.InsertLog(ctx, decisionManagerLog); err != nil {
				log.Errorw("Failed to save Decision Manager Log (sync) after ESA 504", "error", err, "decisionManagerLog", decisionManagerLog)
			} else {
				log.Info("Successfully saved Decision Manager Log (sync) after ESA 504 customerId: ", request.CustomerID)
			}
		} else {
			s.enqueueDecisionManagerLog(ctx, log, decisionManagerLog, request.CustomerID)
		}
	}()
}

func (s *TriggerDecisionService) serviceSfdcFieldMappingFetch(ctx context.Context, valueJson map[string]interface{}, partnerServiceMapping *decision_manager_models.PartnerServiceMappingResponse) ([]utility.SFRequestDB, error) {
	methodName := "serviceSfdcFieldMappingFetch: "
	log := s.logger.WithContext(ctx)

	log.Info("Inside serviceSfdcFieldMappingFetch method")
	var serviceSfdcFieldMapping []*decision_manager_models.ServiceSfdcFieldMappingResponse

	var shouldFetchFromDB bool

	// SequenceArray := utility.ConvertSequenceStringToArray(partnerServiceMapping.ServiceSequenceString)

	// Single pass: build service name array, allowed map and serviceID->name map.
	serviceNameArray, allowedServices, serviceIDToName := extractServiceMetadata(valueJson)
	log.Info("Service Name Array: ", serviceNameArray)

	// Single global key: service_sfdc_field_mapping has no partner column so all partners
	// share the same rows. One key avoids redundant per-partner copies in Redis.
	redisKey := s.RedisKeyGenerator.GenerateRedisKeyForAllServiceSfdcFieldMapping()
	cachedServiceSfdcFieldMapping, redisErr := s.RedisClient.Get(ctx, redisKey)

	// Check if we need to fetch from database
	if redisErr != nil {
		log.Info("Failed to fetch service sfdc field mapping from redis cache, will fetch from database redisKey: ", redisKey, " error: ", redisErr)
		shouldFetchFromDB = true
	} else if len(cachedServiceSfdcFieldMapping) > 0 {
		// Try to unmarshal cached data
		if err := json.Unmarshal([]byte(cachedServiceSfdcFieldMapping), &serviceSfdcFieldMapping); err != nil {
			log.Errorw("Failed to unmarshal cached service sfdc field mapping, will fetch from database", "redisKey", redisKey, "error", err)
			shouldFetchFromDB = true
		} else {
			log.Infow("Service sfdc field mapping fetched from redis cache",
				"redisKey", redisKey,
				"mappingCount", len(serviceSfdcFieldMapping),
				"serviceSfdcFieldMapping", cachedServiceSfdcFieldMapping)
		}
	} else {
		log.Info("Empty cached data, will fetch from database redisKey: ", redisKey)
		shouldFetchFromDB = true
	}

	// Fetch from database if needed
	if shouldFetchFromDB {
		var err error
		// Fetch all active mappings (not filtered by ESA service list) so that the cached
		// blob is stable across requests. Per-request filtering by ESA response is applied
		// in-memory via allowedServices below (lines 928-936).
		serviceSfdcFieldMapping, err = s.ServiceSfdcFieldMappingRepository.FindAll(ctx)
		if err != nil {
			log.Errorw("Error fetching service sfdc field mapping from database", "error", err)
			return nil, err
		}
		//Do not set redis key when there is no entry for given sequenceArray.
		if len(serviceSfdcFieldMapping) == 0 {
			return nil, errors.New("no service sfdc field mapping found")
		}

		response, err := json.Marshal(serviceSfdcFieldMapping)
		if err != nil {
			log.Errorw(methodName, " couldn't update redis as failed to marshal response with err: ", err)
		} else {
			log.Info("Service sfdc field mapping fetched from database: " + string(response))
			err := s.RedisClient.Set(ctx, redisKey, response, s.serviceSfdcFieldMappingTTL)
			if err != nil {
				log.Errorw(methodName, " couldn't update redis as failed to set response with err: ", err)
			}
			log.Info("Set redis key successfull for key: ", redisKey)
		}
	}

	// Filter cached/DB mappings to only include services present in ESA response (non-empty)
	if len(serviceSfdcFieldMapping) > 0 {
		if len(allowedServices) > 0 {
			filtered := serviceSfdcFieldMapping[:0]
			for _, mapping := range serviceSfdcFieldMapping {
				if _, ok := allowedServices[mapping.ServiceName]; ok {
					filtered = append(filtered, mapping)
				}
			}
			serviceSfdcFieldMapping = filtered
		}
	}

	// Transform and merge SF mappings
	serviceOrderIndex := buildServiceOrderIndex(serviceIDToName, partnerServiceMapping.ServiceSequenceString, log)
	if len(serviceOrderIndex) > 0 && len(serviceSfdcFieldMapping) > 1 {
		// Stable sort: ordered services first (by sequence index), others keep relative order at the end
		sort.SliceStable(serviceSfdcFieldMapping, func(i, j int) bool {
			iOrder, iOk := serviceOrderIndex[serviceSfdcFieldMapping[i].ServiceName]
			jOrder, jOk := serviceOrderIndex[serviceSfdcFieldMapping[j].ServiceName]
			switch {
			case iOk && jOk:
				return iOrder < jOrder
			case iOk:
				return true
			case jOk:
				return false
			default:
				return false
			}
		})
		//for debugging merge priority
		orderedServiceNames := make([]string, 0, len(serviceSfdcFieldMapping))
		for _, mapping := range serviceSfdcFieldMapping {
			orderedServiceNames = append(orderedServiceNames, mapping.ServiceName)
		}
		log.Infow("Service mappings ordered for merge priority", "orderedServices", orderedServiceNames)
		//for debugging merge priority ends
	}

	serviceSfdcFieldMappingRequestBodyJson, err := utility.FastMergeServiceSfdcFieldMappingRequestBody(ctx, serviceSfdcFieldMapping)
	if err != nil {
		return nil, err
	}
	log.Info("Service Sfdc Field Mapping Request Body Json serviceSfdcFieldMappingRequestBodyJson: ", serviceSfdcFieldMappingRequestBodyJson)

	return serviceSfdcFieldMappingRequestBodyJson, nil
}

func extractServiceMetadata(valueJson map[string]interface{}) ([]interface{}, map[string]struct{}, map[int]string) {
	serviceNameArray := make([]interface{}, 0, 16)
	allowedServices := make(map[string]struct{}, 16)
	serviceIDToName := make(map[int]string, 16)

	for key, raw := range valueJson {
		serviceData, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasServiceName := serviceData["serviceName"]; !hasServiceName {
			continue
		}
		if isEmptyServiceResponse(serviceData) {
			continue
		}

		serviceNameArray = append(serviceNameArray, key)
		if key != "" {
			allowedServices[key] = struct{}{}
		}

		serviceName, ok := serviceData["serviceName"].(string)
		if !ok || serviceName == "" {
			continue
		}
		serviceID, ok := extractServiceID(serviceData["serviceId"])
		if !ok {
			continue
		}
		if _, exists := serviceIDToName[serviceID]; !exists {
			serviceIDToName[serviceID] = serviceName
		}
	}

	return serviceNameArray, allowedServices, serviceIDToName
}

func buildServiceOrderIndex(serviceIDToName map[int]string, sequenceString string, log contracts.Logger) map[string]int {
	if sequenceString == "" {
		return nil
	}

	orderedIDs := utility.ConvertSequenceStringToArray(sequenceString)
	if len(orderedIDs) == 0 {
		return nil
	}

	if len(serviceIDToName) == 0 {
		if log != nil {
			log.Warnw("Service sequence mapping could not be resolved; using default order", "sequenceString", sequenceString)
		}
		return nil
	}

	serviceOrderIndex := make(map[string]int, len(serviceIDToName))
	for idx, serviceID := range orderedIDs {
		if serviceName, ok := serviceIDToName[serviceID]; ok {
			if _, exists := serviceOrderIndex[serviceName]; !exists {
				serviceOrderIndex[serviceName] = idx
			}
		}
	}

	if len(serviceOrderIndex) == 0 {
		if log != nil {
			log.Warnw("Service sequence order did not match any ESA services; using default order", "sequenceString", sequenceString)
		}
		return nil
	}

	return serviceOrderIndex
}

func extractServiceID(rawID interface{}) (int, bool) {
	switch v := rawID.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n), true
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n, true
		}
	}
	return 0, false
}

func isEmptyServiceResponse(serviceData map[string]interface{}) bool {
	response, ok := serviceData["response"].(map[string]interface{})
	if !ok {
		return false
	}
	body, exists := response["body"]
	if !exists {
		return false
	}
	switch v := body.(type) {
	case map[string]interface{}:
		return len(v) == 0
	case []interface{}:
		return len(v) == 0
	case string:
		return strings.TrimSpace(v) == ""
	case nil:
		return true
	default:
		return false
	}
}

// applyPostExpansionMerge merges array-expanded SFRequest items based on original merge intent
// func (s *TriggerDecisionService) applyPostExpansionMerge(ctx context.Context, expandedItems []utility.SFRequest, originalTemplates []utility.SFRequestDB) []utility.SFRequest {
// 	log := commoninit.GetLogger(ctx)

// 	// Create a map to track which base RefIDs should be merged
// 	shouldMerge := make(map[string]bool)
// 	for _, template := range originalTemplates {
// 		if strings.ToLower(template.Merge) == "true" {
// 			shouldMerge[template.RefID] = true
// 		}
// 	}

// 	// Extract base reference ID (same logic as in utility.go)
// 	extractBaseRefID := func(refID string) string {
// 		if !strings.Contains(refID, "_") {
// 			return refID
// 		}

// 		refID = strings.TrimSuffix(refID, "_merged")

// 		if idx := strings.LastIndexByte(refID, '_'); idx != -1 {
// 			suffix := refID[idx+1:]
// 			if len(suffix) > 0 && len(suffix) <= 2 {
// 				isNumeric := true
// 				for i := 0; i < len(suffix); i++ {
// 					if suffix[i] < '0' || suffix[i] > '9' {
// 						isNumeric = false
// 						break
// 					}
// 				}
// 				if isNumeric {
// 					return refID[:idx]
// 				}
// 			}
// 		}
// 		return refID
// 	}

// 	// Group items by base RefID + URL + Method
// 	type groupKey struct {
// 		BaseRefID string
// 		URL       string
// 		Method    string
// 	}

// 	groups := make(map[groupKey][]utility.SFRequest)
// 	for _, item := range expandedItems {
// 		baseRefID := extractBaseRefID(item.RefID)
// 		key := groupKey{
// 			BaseRefID: baseRefID,
// 			URL:       item.URL,
// 			Method:    item.Method,
// 		}
// 		groups[key] = append(groups[key], item)
// 	}

// 	// Process groups and merge where appropriate
// 	result := make([]utility.SFRequest, 0, len(expandedItems))
// 	for key, items := range groups {
// 		if len(items) > 1 && shouldMerge[key.BaseRefID] {
// 			// Merge multiple items with same base RefID, URL, Method
// 			mergedBody := make(map[string]interface{})
// 			for _, item := range items {
// 				for k, v := range item.Body {
// 					mergedBody[k] = v // Later values overwrite earlier ones
// 				}
// 			}

// 			// Create merged request with first item's RefID + _merged suffix
// 			mergedRequest := utility.SFRequest{
// 				URL:    items[0].URL,
// 				Method: items[0].Method,
// 				RefID:  fmt.Sprintf("%s_merged", items[0].RefID),
// 				Body:   mergedBody,
// 			}

// 			result = append(result, mergedRequest)
// 			log.Info(fmt.Sprintf("Post-expansion merge: merged %d items with baseRefID=%s, url=%s, method=%s → %s",
// 				len(items), key.BaseRefID, key.URL, key.Method, mergedRequest.RefID))
// 		} else {
// 			// Keep items separate
// 			result = append(result, items...)
// 		}
// 	}

// 	return result
// }
