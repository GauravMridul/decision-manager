package constants

const (

	// DMI base url key
	DmiBaseUrlKey   = "DmiBaseUrl"
	Environment     = "Environment"
	Production      = "production"
	DecisionManager = "/decision-manager"

	EsaBaseUrlKey = "EsaBaseUrl"

	//HTTP methods
	POST  = "POST"
	GET   = "GET"
	PATCH = "PATCH"
	PUT   = "PUT"

	// Swagger
	SwaggerDocJSON = "/swagger/doc.json"

	// Token header
	ContentType                = "Content-Type"
	Accept                     = "accept"
	ApplicationJsonContentType = "application/json"
	Authorization              = "Authorization"
	ApiKey                     = "X-Api-Key"
	Bearer                     = "Bearer "
	CorrelationId              = "X-Correlation-ID"
	RealIpHeaderKey            = "X-REAL-IP"
	ForwardedForHeaderKey      = "X-FORWARDED-FOR"
	RemoteAddressHeaderKey     = "REMOTE-ADDR"
	IpAddress                  = "ip-address"
	ProcessModeHeaderKey       = "X-Process-Mode"
	// Comma delimiter
	CommaDelimiter = ","

	EmptyString      = ""
	UnderscoreString = "_"

	//Environment keys
	EnvKey                = "BOOT_CUR_ENV"
	DevEnvironment        = "dev"
	Service               = "esa"
	DevConfigJsonFilePath = "configs/config.json"

	// Server
	ServiceName    = "service.name"
	RedisURL       = "redis.url"
	LoggerLevelKey = "log.level"
	Region         = "aws.region"
	ServerPort     = "server.port"
	EnvironmentKey = "Environment"
	// Enum constants
	UnknownEnumValue = "Unknown"

	StringType  = "string"
	BooleanType = "bool"
	FloatType   = "float"
	Json        = "json"

	//API Paths
	ExternalServiceAdapter = "external-service-adapter/v1/process-sequence/v2"

	//API Keys
	EsaApiKeyKey = "EsaApiKey"

	// ESA API: max connection retries on connection/transport errors only (not on timeout). 0 = no retry.
	EsaConnectionRetryCountKey = "esa.connection_retry_count"

	// Audit log: when true, MongoDB audit log insert is synchronous (block response until written, default is false)
	AuditLogSyncKey = "audit.log.sync"

	// Optional cap on in-flight trigger decisions (0 = no cap)
	ServerMaxConcurrentTriggerDecisions = "server.max_concurrent_trigger_decisions"

	// Cap on parallel goroutines per request in dynamic_json_updater (0 = no cap)
	ServerMaxParallelDynamicJsonGoroutines = "server.max_parallel_dynamic_json_goroutines"
	// Global cap on dynamic_json_updater worker goroutines across all requests (0 = no cap)
	ServerMaxGlobalWorkerGoroutines = "server.max_global_worker_goroutines"
	// Cap on template-level goroutines in dynamic_json_updater (0 = auto/min with inner cap)
	ServerMaxParallelDynamicJsonTemplateGoroutines = "server.max_parallel_dynamic_json_template_goroutines"
	// Cap on expression cache entries in dynamic_json_updater
	ServerDynamicJsonExprCacheMaxEntries = "server.dynamic_json_expr_cache_max_entries"
	// Cap on parameter cache entries in dynamic_json_updater
	ServerDynamicJsonParameterCacheMaxEntries = "server.dynamic_json_parameter_cache_max_entries"

	// Async DM audit log queue/worker keys
	AuditLogAsyncDMWorkersKey        = "audit.log.async.dm_workers"
	AuditLogAsyncDMQueueSizeKey      = "audit.log.async.dm_queue_size"
	AuditLogAsyncDMTimeoutSecondsKey = "audit.log.async.dm_timeout_seconds"

	// Graceful shutdown timeout keys
	ServerShutdownHTTPTimeoutSecondsKey    = "server.shutdown.http_timeout_seconds"
	ServerShutdownModulesTimeoutSecondsKey = "server.shutdown.modules_timeout_seconds"
	ServerShutdownTestEndpointEnabledKey   = "server.shutdown.test_endpoint.enabled"
	ServerShutdownTestEndpointDelayMsKey   = "server.shutdown.test_endpoint.delay_ms"

	// Runtime telemetry keys (low-overhead operational diagnostics)
	TelemetryEnabledKey                    = "telemetry.enabled"
	TelemetryIntervalSecondsKey            = "telemetry.interval_seconds"
	TelemetryMemstatsEnabledKey            = "telemetry.memstats.enabled"
	TelemetryCacheSampleEnabledKey         = "telemetry.cache_sample.enabled"
	TelemetryCacheSampleIntervalSecondsKey = "telemetry.cache_sample.interval_seconds"
	TelemetryCacheSampleMaxEntriesKey      = "telemetry.cache_sample.max_entries"
)

const (
	DefaultAsyncDMLogWorkers          = 2
	DefaultAsyncDMLogQueueSize        = 1000
	DefaultAsyncDMLogTimeoutSeconds   = 5
	DefaultAsyncDMLogInlineFallbackMs = 200

	DefaultShutdownHTTPTimeoutSeconds    = 30
	DefaultShutdownModulesTimeoutSeconds = 15
	DefaultShutdownTestEndpointDelayMs    = 200
	DefaultMaxParallelTemplateGoroutines       = 20
	DefaultTelemetryIntervalSeconds            = 60
	DefaultTelemetryCacheSampleIntervalSeconds = 300
	DefaultTelemetryCacheSampleMaxEntries      = 20
	// Default ESA connection retries on connection reset/refused (not on timeout). 2 = up to 3 attempts.
	DefaultEsaConnectionRetryCount = 2
)

const (
	JWKS_AUDIENCE = "JwksAudience"
	JWKS_ISSUER   = "JwksIssuer"
	JWKS_URL      = "JwksUrl"
)

const (
	// Database configuration
	DatabaseUserName         = "database.username"
	DatabasePassword         = "database.password"
	DatabaseHost             = "database.host"
	DatabaseName             = "database.name"
	DatabasePort             = "database.port"
	DatabaseDialect          = "database.dialect"
	DbMigrationDir           = "/internal/app/db/migrations"
	MaxIdleConnections       = "database.maxIdleConnections"
	MaxOpenConnections       = "database.maxOpenConnections"
	ConnMaxLifetimeInHours   = "database.connMaxLifetimeInHours"
	ShouldRunAutoMigrations  = "database.shouldRunAutoMigrations"
	ShouldRunGooseMigrations = "database.shouldRunGooseMigrations"
)
