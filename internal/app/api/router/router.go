package router

import (
	"decision-manager/internal/app/constants"
	"decision-manager/internal/app/controller"
	"decision-manager/internal/app/db"
	"decision-manager/internal/app/db/repository"
	"decision-manager/internal/app/service/cache_service"
	"decision-manager/internal/app/service/trigger_decision_service"
	utilityClient "decision-manager/internal/app/utility"

	// "esa/internal/app/errorhandling/errors_mapping"
	// "esa/internal/app/service/module_level_services"
	// "esa/internal/app/service/service_clients"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/internal/app/utility"
	"decision-manager/middleware/auth"
	"decision-manager/pkg/client"
	token_service "decision-manager/pkg/token"

	"github.com/dmi-infotech/common-modules/go/contracts"

	"github.com/go-playground/validator/v10"

	"strings"

	"github.com/google/uuid"

	// "github.com/spf13/commoninit"

	"github.com/gin-gonic/gin"
	cors "github.com/rs/cors/wrapper/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// @title Swagger UI
// @version 1.0
// @description Swagger UI for decision manager Microservice.
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html
// @BasePath /decision-manager

// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-Api-Key

/*
	Note: You have to run "swag init -g internal/app/api/router/router.go"
		  after updating any annotation comments either here or in any api's
*/

// NewRouter :
func NewRouter() *gin.Engine {

	// Getting service Instance
	requestValidatorUtility, requestValidator, dynamicJsonUpdater, apiClient, tokenService, serviceSfdcFieldMappingRepository, partnerServiceMappingRepository, redisKeyGenerator := InitServicesInstance()

	// Init Controller
	triggerDecisionController, cacheController := InitControllerForRouter(requestValidatorUtility, requestValidator, dynamicJsonUpdater, apiClient, tokenService, serviceSfdcFieldMappingRepository, partnerServiceMappingRepository, redisKeyGenerator)
	env := commoninit.GetConfigString(constants.Environment)

	//Initialize Router and Add few metadata
	router := InitRouterAndAddMetaDataInRouter(env)

	// Grouping of router path
	decisionManager := router.Group(constants.DecisionManager)
	{
		v1 := decisionManager.Group(constants.VersionV1)
		{
			v1.Use(auth.AuthManager.AuthMiddleware())

			// Add Hello World route - no auth required
			v1.GET("/hello", triggerDecisionController.HelloWorld)
			v1.POST("/trigger-decision", triggerDecisionController.TriggerDecision())
			v1.GET("/cache/ping", cacheController.PingCache())
			v1.GET("/cache/stats", cacheController.GetCacheStats())
			v1.GET("/cache/value", cacheController.GetCacheValueByKey())
			v1.POST("/cache/clear", cacheController.ClearCache)

			// Test-only endpoint to trigger graceful shutdown via SIGTERM.
			// Disabled by default and blocked in production by controller checks.
			shutdownController := new(controller.ShutdownController)
			v1.POST("/graceful-shutdown", shutdownController.TriggerGracefulShutdown)

		}
	}
	return router
}

func InitRouterAndAddMetaDataInRouter(env string) *gin.Engine {
	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(uuidInjectionMiddleware())
	allowedOrigins := commoninit.GetConfigString(constants.AllowedOrigins)
	allowedOriginsList := strings.Split(allowedOrigins, constants.CommaDelimiter)
	corsHandler := cors.New(cors.Options{
		AllowedOrigins: allowedOriginsList,
		AllowedMethods: []string{constants.GET, constants.POST, constants.PUT, constants.PATCH},
		AllowedHeaders: []string{"*"},
	})
	router.Use(corsHandler)
	health := new(controller.HealthController)
	router.GET(constants.HealthCheckPathUrl, health.Status)

	// Swagger Implementation
	if !strings.EqualFold(env, constants.Production) {
		swaggerUrl := commoninit.GetConfigString(constants.DmiBaseUrlKey) + constants.DecisionManager + constants.SwaggerDocJSON
		url := ginSwagger.URL(swaggerUrl) // The url pointing to API definition
		router.GET(constants.SwaggerPathUrl, ginSwagger.WrapHandler(swaggerFiles.Handler, url))
	}
	return router
}

func InitServicesInstance() (*utility.RequestValidator, *validator.Validate, *utility.DynamicJsonUpdater, contracts.APIClient, *token_service.TokenService, *repository.ServiceSfdcFieldMappingRepositoryImpl, *repository.PartnerServiceMappingRepositoryImpl, *utilityClient.RedisKeyGenerator) {
	// log1 := logger.GetLogger(context.Background())
	redisKeyGenerator := utilityClient.NewRedisKeyGenerator()
	// redisClient := cache.NewRedisRegularClient(log1, commoninit.GetConfigString(constants.RedisUrlKey), constants.DecisionManager)
	requestValidatorUtility := utility.NewRequestValidator()
	requestValidator := utility.NewValidator()
	dynamicJsonUpdater := utility.NewDynamicJsonUpdater()
	apiClient := commoninit.GetAPIClient()
	serviceSfdcFieldMappingRepository := repository.NewServiceSfdcFieldMappingRepositoryImpl()
	partnerServiceMappingRepository := repository.NewPartnerServiceMappingRepositoryImpl()
	// errorMap := errors_mapping.NewCustomStatusMap()
	// httpStatusMap := errors_mapping.NewHttpStatusMap()
	// customStatusMap := errors_mapping.NewCustomStatusMap()
	tokenService := token_service.NewTokenService()
	// return requestValidatorUtility, requestValidator, apiClient, errorMap, httpStatusMap, customStatusMap, tokenService
	return requestValidatorUtility, requestValidator, dynamicJsonUpdater, apiClient, tokenService, serviceSfdcFieldMappingRepository, partnerServiceMappingRepository, redisKeyGenerator
}

func InitControllerForRouter(requestValidatorUtility *utility.RequestValidator, requestValidator *validator.Validate, dynamicJsonUpdater *utility.DynamicJsonUpdater, apiClient contracts.APIClient, tokenService *token_service.TokenService, serviceSfdcFieldMappingRepository *repository.ServiceSfdcFieldMappingRepositoryImpl, partnerServiceMappingRepository *repository.PartnerServiceMappingRepositoryImpl, redisKeyGenerator *utilityClient.RedisKeyGenerator) (*controller.TriggerDecisionController, *controller.CacheController) {
	// IMPORTANT: Initialize the Salesforce service first
	salesforceService := &client.SalesforceService{}
	// Force initialization of the Salesforce client - this creates the singleton
	salesforceService.InitializeSalesforceClient(nil)
	// Now get the client from the service
	salesforceClient := salesforceService.GetSalesforceClient()

	// Initialize DecisionManagerLogRepository
	decisionManagerLogRepository := repository.NewDecisionManagerLogRepository()

	// Create the service with the Salesforce client directly and the log repository
	triggerDecisionService := trigger_decision_service.NewTriggerDecisionService(dynamicJsonUpdater, serviceSfdcFieldMappingRepository, salesforceClient, partnerServiceMappingRepository, apiClient, decisionManagerLogRepository, redisKeyGenerator)
	triggerDecisionController := controller.NewTriggerDecisionController(requestValidator, requestValidatorUtility, apiClient, triggerDecisionService)
	cacheService := cache_service.NewCacheService()
	cacheController := controller.NewCacheController(cacheService)

	if commoninit.IsShutdownManagerAvailable() {
		commoninit.GetShutdownManager().AddShutdownHook(triggerDecisionService.Close)
		commoninit.GetShutdownManager().AddShutdownHook(db.DisconnectMongoDB)
	}

	return triggerDecisionController, cacheController
}

func uuidInjectionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		correlationId := c.GetHeader(constants.CorrelationId)
		if len(correlationId) == 0 {
			correlationID, _ := uuid.NewUUID()
			correlationId = correlationID.String()
			c.Request.Header.Set(constants.CorrelationId, correlationId)
		}
		c.Writer.Header().Set(constants.CorrelationId, correlationId)

		c.Next()
	}
}
