package controller

import (
	"crypto/subtle"
	"decision-manager/internal/app/constants"
	"decision-manager/internal/app/dto/request_dto/decision_manager_request_dto"
	"decision-manager/internal/app/service/trigger_decision_service"
	"decision-manager/internal/app/utility"
	"decision-manager/pkg/correlation"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	commoninit "decision-manager/internal/app/init"

	"strings"

	"github.com/dmi-infotech/common-modules/go/contracts"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type TriggerDecisionController struct {
	Validator              *validator.Validate
	Utility                *utility.RequestValidator
	TriggerDecisionService trigger_decision_service.ITriggerDecisionService
}

func NewTriggerDecisionController(validator *validator.Validate, requestValidator *utility.RequestValidator, apiClient contracts.APIClient, triggerDecisionService trigger_decision_service.ITriggerDecisionService) *TriggerDecisionController {
	controller := &TriggerDecisionController{
		Validator:              validator,
		Utility:                requestValidator,
		TriggerDecisionService: triggerDecisionService,
	}
	return controller
}

// HelloWorld handles GET request and returns a simple Hello World response
// @Summary Hello World API
// @Description Returns a Hello World message
// @Tags Decision Manager
// @Accept json
// @Produce json
// @Success 200 {object} map[string]string
// @Router /v1/hello [get]
func (s *TriggerDecisionController) HelloWorld(c *gin.Context) {
	response := map[string]string{"message": "Hello World from Decision Manager!"}
	c.JSON(http.StatusOK, response)
}

// TriggerDecision triggers decisionioning process
// @Summary Triggers decisioning process
// @Description Triggers a decisioning process with the provided parameters
// @Tags Decision Manager
// @Accept json
// @Produce json
// @Param request body decision_manager_request_dto.TriggerDecisionRequest true "Sequence details"
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string "BadRequest"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 404 "NotFound"
// @Failure 500 "InternalServerError"
// @Router /v1/trigger-decision [post]
func (t *TriggerDecisionController) TriggerDecision() gin.HandlerFunc {
	return func(c *gin.Context) {

		ctx := correlation.WithReqContext(c)
		ctx = utility.AddIPHeadersToContext(c, ctx)
		log := commoninit.GetLogger(ctx)
		log.Info("Inside Trigger decision controller method")

		headers := make(map[string]string)
		headers[constants.CorrelationId], _ = correlation.FromContext(ctx)

		token := c.GetHeader(constants.ApiKey)
		expectedKey := commoninit.GetConfigString(constants.ApiKey)
		// Empty configured key must reject all requests: otherwise len("") == len("") and
		// ConstantTimeCompare on two empty slices succeeds (authentication bypass).
		if expectedKey == "" || len(token) != len(expectedKey) || subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			return
		}

		var request decision_manager_request_dto.TriggerDecisionRequest
		// Bind and validate request
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		requestStartTime := time.Now()
		correlationId := headers[constants.CorrelationId]
		reqLogger := commoninit.GetLogger(ctx).WithFields(map[string]interface{}{
			"correlation_id": correlationId,
			"lead_id":        request.LeadID,
			"stage":          request.Stage,
		})
		ctx = utility.SetRequestStartTime(ctx, requestStartTime)
		ctx = utility.SetRequestLogger(ctx, reqLogger)
		log = reqLogger // use request-scoped logger so lead_id/stage appear in all subsequent logs
		reqLogger.Infow("Checkpoint", "checkpoint", "request_received", "elapsed_since_start_ms", time.Since(requestStartTime).Milliseconds())

		if !commoninit.TryAcquireTriggerDecision() {
			log.Warnw("Trigger decision rejected: max concurrent limit reached")
			c.Header("Retry-After", "10")
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   "Service temporarily overloaded; retry after Retry-After seconds",
				"message": "max concurrent trigger decisions limit reached",
			})
			return
		}

		processMode := c.GetHeader(constants.ProcessModeHeaderKey)
		forceSync := strings.EqualFold(processMode, "sync")

		if forceSync {
			defer commoninit.ReleaseTriggerDecision()
			log.Info("Processing trigger decision synchronously (header override)")
			_, err := t.TriggerDecisionService.TriggerDecision(ctx, &request, headers)
			if err != nil {
				log.Error("Error processing trigger decision synchronously: " + err.Error())
				c.JSON(http.StatusInternalServerError, gin.H{
					"message": "Processing failed",
					"error":   err.Error(),
				})
				return
			}

			c.JSON(http.StatusOK, gin.H{
				"message": "Processed successfully",
				"status":  "completed",
			})
			return
		}

		// Extract all values from Gin context BEFORE responding, since Gin
		// recycles *gin.Context after the handler returns.
		clientIP := c.ClientIP()

		// Return success response immediately
		c.JSON(http.StatusOK, gin.H{"message": "Request received successfully", "status": "processing"})

		// Process trigger decision service in background
		req := request
		startTime := requestStartTime
		rlog := reqLogger
		commoninit.StartTrackedGoroutine(func() {
			defer commoninit.ReleaseTriggerDecision()
			defer func() {
				if r := recover(); r != nil {
					rlog.Errorw("Recovered panic in trigger decision background processor",
						"panic", r,
						"error", fmt.Sprintf("%v", r),
						"stackTrace", string(debug.Stack()))
				}
			}()
			backgroundCtx, _ := correlation.NewContext(headers[constants.CorrelationId])
			backgroundCtx = utility.AddIPToContext(backgroundCtx, clientIP)
			backgroundCtx = utility.SetRequestStartTime(backgroundCtx, startTime)
			backgroundCtx = utility.SetRequestLogger(backgroundCtx, rlog)

			rlog.Info("Processing trigger decision in background")
			_, err := t.TriggerDecisionService.TriggerDecision(backgroundCtx, &req, headers)
			if err != nil {
				rlog.Error("Error processing trigger decision in background: " + err.Error())
			} else {
				rlog.Info("Successfully processed trigger decision in background")
			}
		})
	}
}
