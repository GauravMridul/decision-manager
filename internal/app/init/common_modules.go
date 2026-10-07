package init

import (
	"context"
	"decision-manager/internal/app/constants"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dmi-infotech/common-modules/go/contracts"
	"github.com/dmi-infotech/common-modules/go/http/client"
	"github.com/dmi-infotech/common-modules/go/infrastructure/cache"
	"github.com/dmi-infotech/common-modules/go/infrastructure/config"
	"github.com/dmi-infotech/common-modules/go/infrastructure/logger"
	"github.com/dmi-infotech/common-modules/go/infrastructure/shutdown"
	cmcontext "github.com/dmi-infotech/common-modules/go/utils/context"
	"github.com/spf13/viper"
)

// CommonModulesConfig defines which components should be initialized
type CommonModulesConfig struct {
	EnableLogger          bool
	EnableConfig          bool
	EnableCache           bool
	EnableAPIClient       bool
	EnableShutdownManager bool
	EnableCacheKeyBuilder bool
	EnableUtilityProvider bool
}

// DefaultCommonModulesConfig returns the default configuration for Decision Manager
func DefaultCommonModulesConfig() CommonModulesConfig {
	return CommonModulesConfig{
		EnableLogger:          true,
		EnableConfig:          true,
		EnableCache:           true,
		EnableAPIClient:       true,
		EnableShutdownManager: true,
		EnableCacheKeyBuilder: true,
		EnableUtilityProvider: true,
	}
}

// DecisionManagerCacheConfig defines the cache configuration specific to Decision Manager
type DecisionManagerCacheConfig struct {
	Host                       string        `json:"host" mapstructure:"host"`
	Port                       int           `json:"port" mapstructure:"port"`
	Password                   string        `json:"password" mapstructure:"password"`
	Database                   int           `json:"database" mapstructure:"database"`
	PoolSize                   int           `json:"pool_size" mapstructure:"pool_size"`
	MinIdleConns               int           `json:"min_idle_conns" mapstructure:"min_idle_conns"`
	MaxRetries                 int           `json:"max_retries" mapstructure:"max_retries"`
	DialTimeout                time.Duration `json:"dial_timeout" mapstructure:"dial_timeout"`
	ReadTimeout                time.Duration `json:"read_timeout" mapstructure:"read_timeout"`
	WriteTimeout               time.Duration `json:"write_timeout" mapstructure:"write_timeout"`
	PoolTimeout                time.Duration `json:"pool_timeout" mapstructure:"pool_timeout"`
	DefaultTTL                 time.Duration `json:"default_ttl" mapstructure:"default_ttl"`
	PartnerServiceMappingTTL   time.Duration `json:"partner_service_mapping_ttl" mapstructure:"partner_service_mapping_ttl"`
	QueryObjectTTL             time.Duration `json:"query_object_ttl" mapstructure:"query_object_ttl"`
	ServiceSFDCFieldMappingTTL time.Duration `json:"service_sfdc_field_mapping_ttl" mapstructure:"service_sfdc_field_mapping_ttl"`
	Enabled                    bool          `json:"enabled" mapstructure:"enabled"`
	Compression                bool          `json:"compression" mapstructure:"compression"`
}

// CommonModules holds all initialized common modules components
type CommonModules struct {
	Logger                     contracts.Logger
	Config                     contracts.ConfigProvider
	ConfigWatcher              *config.VolumeConfigWatcher // New: Volume watcher
	Cache                      contracts.CacheProvider
	APIClient                  contracts.APIClient
	UtilityProvider            contracts.UtilityProvider
	ShutdownManager            *shutdown.Manager
	CacheKeyBuilder            contracts.CacheKeyBuilder
	DecisionManagerCacheConfig *DecisionManagerCacheConfig
}

// GlobalModules holds the initialized common modules
var GlobalModules *CommonModules
var initMutex sync.RWMutex
var isInitialized bool
var trackedGoroutines sync.WaitGroup
var trackedGoroutineCount atomic.Int64
var isShuttingDown bool
var appShutdownSignal = make(chan struct{})
var appShutdownSignalCloseOnce sync.Once

var triggerDecisionLimiter chan struct{}
var triggerDecisionLimiterOnce sync.Once

func StartTrackedGoroutine(fn func()) {
	if fn == nil {
		return
	}
	trackedGoroutineCount.Add(1)
	trackedGoroutines.Add(1)
	go func() {
		defer trackedGoroutineCount.Add(-1)
		defer trackedGoroutines.Done()
		fn()
	}()
}

func WaitForTrackedGoroutines(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Polling avoids spawning a waiter goroutine that can linger after context timeout.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if trackedGoroutineCount.Load() == 0 {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func GetAppShutdownSignal() <-chan struct{} {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return appShutdownSignal
}

// DecisionManagerUtilityProvider implements contracts.UtilityProvider for Decision Manager-specific functionality
type DecisionManagerUtilityProvider struct{}

// GetForwardedIP returns the forwarded IP from context
func (d *DecisionManagerUtilityProvider) GetForwardedIP(ctx context.Context) string {
	// Inline the ContextForwardedIP functionality to avoid import cycle
	if ctx == nil {
		return ""
	}

	// Try to get the forwarded IP from context
	if ip, ok := ctx.Value(constants.ForwardedForHeaderKey).(string); ok {
		return ip
	}

	// Try alternative context key
	if ip, ok := ctx.Value("client_ip").(string); ok {
		return ip
	}

	return ""
}

// NoOpCacheProvider is a cache provider that does nothing when cache is not available
type NoOpCacheProvider struct{}

func (n *NoOpCacheProvider) Get(ctx context.Context, key string) (string, error) {
	return "", nil // NoOp: empty data, no error — caller treats as cache miss
}

func (n *NoOpCacheProvider) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return nil
}

func (n *NoOpCacheProvider) Delete(ctx context.Context, key string) error {
	return nil
}

func (n *NoOpCacheProvider) Exists(ctx context.Context, key string) (bool, error) {
	return false, nil
}

func (n *NoOpCacheProvider) MGet(ctx context.Context, keys []string) ([]string, error) {
	return nil, nil
}

func (n *NoOpCacheProvider) MSet(ctx context.Context, keyValues map[string]string, ttl time.Duration) error {
	return nil
}

func (n *NoOpCacheProvider) MDelete(ctx context.Context, keys []string) error {
	return nil
}

func (n *NoOpCacheProvider) HGet(ctx context.Context, key, field string) (string, error) {
	return "", nil
}

func (n *NoOpCacheProvider) HSet(ctx context.Context, key, field, value string, ttl time.Duration) error {
	return nil
}

func (n *NoOpCacheProvider) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return nil, nil
}

func (n *NoOpCacheProvider) HDelete(ctx context.Context, key string, fields ...string) error {
	return nil
}

func (n *NoOpCacheProvider) Ping(ctx context.Context) error {
	return nil
}

func (n *NoOpCacheProvider) FlushAll(ctx context.Context) error {
	return nil
}

func (n *NoOpCacheProvider) Close() error {
	return nil
}

func (n *NoOpCacheProvider) GetStats(ctx context.Context) (contracts.CacheStats, error) {
	return contracts.CacheStats{}, nil
}

// InitializeCommonModulesDefault initializes common modules with default Decision Manager configuration
func InitializeCommonModulesDefault() error {
	return InitializeCommonModules(DefaultCommonModulesConfig())
}

// InitializeCommonModules initializes common modules with the specified configuration
func InitializeCommonModules(config CommonModulesConfig) error {
	initMutex.Lock()
	defer initMutex.Unlock()

	if isInitialized {
		return fmt.Errorf("common modules already initialized")
	}

	// Reset app-level shutdown signal for a fresh lifecycle.
	appShutdownSignal = make(chan struct{})
	appShutdownSignalCloseOnce = sync.Once{}
	isShuttingDown = false

	fmt.Println("🚀 Initializing Common Modules for Decision Manager...")

	// Initialize the global modules container
	GlobalModules = &CommonModules{}

	var err error

	// Step 1: Initialize Logger first for ConfigWatcher logging
	if config.EnableLogger {
		fmt.Println("📝 Initializing Basic Logger...")
		// Initialize with basic configuration first
		GlobalModules.Logger, err = initializeBasicLogger()
		if err != nil {
			return fmt.Errorf("failed to initialize basic logger: %w", err)
		}
		// Test log to confirm basic logger is working and logging to stdout
		GlobalModules.Logger.Info("Basic logger initialized and ready for common-modules logging",
			"output_paths", "stdout",
			"error_output_paths", "stderr")
		fmt.Println("✅ Basic Logger initialized successfully")
	}

	// Step 2: Initialize Configuration Provider with ConfigMap watcher
	if config.EnableConfig {
		fmt.Println("📋 Initializing Configuration Provider with ConfigMap watcher...")
		GlobalModules.Config, GlobalModules.ConfigWatcher, err = initializeConfigWithWatcher(GlobalModules.Logger)
		if err != nil {
			return fmt.Errorf("failed to initialize config: %w", err)
		}

		// Update logger with full configuration now that config is loaded
		if config.EnableLogger {
			fmt.Println("📝 Updating Logger with configuration...")
			GlobalModules.Logger, err = initializeLogger(GlobalModules.Config)
			if err != nil {
				return fmt.Errorf("failed to update logger with config: %w", err)
			}
		}

		fmt.Println("✅ Configuration Provider initialized successfully")
	}

	// Step 3: Initialize Utility Provider
	if config.EnableUtilityProvider {
		fmt.Println("🔧 Initializing Utility Provider...")
		GlobalModules.UtilityProvider = &DecisionManagerUtilityProvider{}
		fmt.Println("✅ Utility Provider initialized successfully")
	}

	// Step 4: Initialize Decision Manager Cache Configuration
	if config.EnableCache {
		fmt.Println("⚙️  Initializing Decision Manager Cache Configuration...")
		GlobalModules.DecisionManagerCacheConfig, err = initializeDecisionManagerCacheConfig(GlobalModules.Config)
		if err != nil {
			if GlobalModules.Logger != nil {
				GlobalModules.Logger.Warnw("Failed to initialize Decision Manager cache configuration", "error", err)
			}
			fmt.Printf("⚠️  Decision Manager cache configuration initialization failed: %v\n", err)
		} else {
			fmt.Println("✅ Decision Manager Cache Configuration initialized successfully")
		}
	}

	// Step 5: Initialize Cache Key Builder
	if config.EnableCacheKeyBuilder {
		fmt.Println("🔑 Initializing Cache Key Builder...")
		GlobalModules.CacheKeyBuilder = cache.NewCacheKeyBuilder("decision-manager")
		fmt.Println("✅ Cache Key Builder initialized successfully")
	}

	// Step 6: Initialize Cache Provider
	if config.EnableCache {
		fmt.Println("🗄️  Initializing Cache Provider...")
		GlobalModules.Cache, err = initializeCache(GlobalModules.Config, GlobalModules.Logger)
		if err != nil {
			// Log warning but don't fail initialization
			if GlobalModules.Logger != nil {
				GlobalModules.Logger.Warnw("Failed to initialize cache, continuing without cache", "error", err)
			}
			fmt.Printf("⚠️  Cache initialization failed, continuing without cache: %v\n", err)
			// CRITICAL FIX: Set cache to nil to prevent shutdown panic
			GlobalModules.Cache = nil
		} else {
			fmt.Println("✅ Cache Provider initialized successfully")
		}
	}

	// Step 6: Initialize API Client
	if config.EnableAPIClient {
		fmt.Println("🌐 Initializing API Client...")
		GlobalModules.APIClient, err = initializeAPIClient(GlobalModules.Config, GlobalModules.Logger, GlobalModules.UtilityProvider)
		if err != nil {
			return fmt.Errorf("failed to initialize API client: %w", err)
		}
		fmt.Println("✅ API Client initialized successfully")
	}

	// Step 7: Initialize Shutdown Manager
	if config.EnableShutdownManager {
		fmt.Println("🛑 Initializing Shutdown Manager...")
		GlobalModules.ShutdownManager, err = initializeShutdownManager(GlobalModules.Config, GlobalModules.Logger)
		if err != nil {
			return fmt.Errorf("failed to initialize shutdown manager: %w", err)
		}
		fmt.Println("✅ Shutdown Manager initialized successfully")
	}

	isInitialized = true
	fmt.Println("🎉 Common Modules initialization completed successfully!")

	// Log initialization summary
	if GlobalModules.Logger != nil {
		GlobalModules.Logger.Infow("Common modules initialized",
			"logger", config.EnableLogger,
			"config", config.EnableConfig,
			"cache", config.EnableCache && GlobalModules.Cache != nil,
			"apiClient", config.EnableAPIClient,
			"shutdownManager", config.EnableShutdownManager,
			"cacheKeyBuilder", config.EnableCacheKeyBuilder,
			"utilityProvider", config.EnableUtilityProvider,
			"decisionManagerCacheConfig", config.EnableCache && GlobalModules.DecisionManagerCacheConfig != nil,
			"configWatcher", GlobalModules.ConfigWatcher != nil)
	}

	// Step 8: Start ConfigMap watching if enabled
	if GlobalModules.ConfigWatcher != nil {
		fmt.Println("👁️  Starting ConfigMap watcher...")
		if err := GlobalModules.ConfigWatcher.StartWatching(); err != nil {
			GlobalModules.Logger.Warnw("Failed to start ConfigMap watcher", "error", err)
		} else {
			fmt.Println("✅ ConfigMap watcher started successfully")
		}

		// Register configuration reload callback.
		// The callback fires from the ConfigWatcher goroutine, so we must
		// acquire initMutex to avoid a data race with concurrent readers.
		GlobalModules.ConfigWatcher.OnConfigReload(func(newConfig contracts.ConfigProvider) {
			initMutex.Lock()
			defer initMutex.Unlock()

			if GlobalModules == nil {
				return
			}
			GlobalModules.Config = newConfig
			if GlobalModules.Logger != nil {
				GlobalModules.Logger.Infow("Configuration reloaded successfully")
			}
		})
	}

	return nil
}

// initializeBasicLogger initializes a basic logger without configuration
func initializeBasicLogger() (contracts.Logger, error) {
	logConfig := logger.Config{
		Level:       logger.LogLevel("info"),
		Development: false,
		Encoding:    "json",
		// Note: OutputPaths and ErrorOutputPaths will default to ["stdout"] and ["stderr"] in NewZapLogger
	}

	return logger.NewZapLogger(logConfig)
}

// initializeConfigWithWatcher initializes the configuration provider with ConfigMap watcher
// func initializeConfigWithWatcher(logger contracts.Logger) (contracts.ConfigProvider, *config.K8sConfigWatcher, error) {
// 	// Determine environment
// 	env := os.Getenv("ENVIRONMENT")
// 	if env == "" {
// 		env = os.Getenv("ENV")
// 		if env == "" {
// 			env = "dev" // Default to dev environment
// 		}
// 	}

// 	// Determine namespace for ConfigMap
// 	namespace := getNamespaceForEnvironment(env)

// 	fmt.Printf("Loading configuration for environment: %s, namespace: %s\n", env, namespace)

// 	// Prepare secret resolver options if AWS secrets are enabled
// 	var secretResolverOptions *config.SecretResolverOptions
// 	if enableSecrets := os.Getenv("ENABLE_AWS_SECRETS"); enableSecrets == "true" {
// 		secretResolverOptions = &config.SecretResolverOptions{
// 			Region:       os.Getenv("AWS_REGION"),
// 			SecretPrefix: os.Getenv("SECRET_MANAGER_PREFIX"),
// 			Logger:       logger,
// 		}
// 		fmt.Println("🔐 AWS Secret resolution enabled")
// 	}

// 	// Try to initialize ConfigMap watcher
// 	watcherOptions := config.ConfigWatcherOptions{
// 		ServiceName:           "decision-manager",
// 		Environment:           env,
// 		Namespace:             namespace,
// 		Logger:                logger,
// 		SecretResolverOptions: secretResolverOptions,
// 	}

// 	watcher, err := config.NewK8sConfigWatcher(watcherOptions)
// 	if err != nil {
// 		fmt.Printf("⚠️  Failed to initialize ConfigMap watcher: %v\n", err)
// 		fmt.Println("📁 Falling back to file-based configuration...")

// 		// Fallback to file-based configuration
// 		configProvider, fallbackErr := initializeFallbackConfig(env)
// 		if fallbackErr != nil {
// 			return nil, nil, fmt.Errorf("both ConfigMap and file-based configuration failed: ConfigMap error: %v, Fallback error: %v", err, fallbackErr)
// 		}

// 		return configProvider, nil, nil
// 	}

// 	// Get initial configuration from watcher
// 	configProvider := watcher.GetConfig()
// 	if configProvider == nil {
// 		return nil, nil, fmt.Errorf("failed to get initial configuration from ConfigMap watcher")
// 	}

// 	fmt.Println("✅ ConfigMap-based configuration loaded successfully")
// 	return configProvider, watcher, nil
// }

func initializeConfigWithWatcher(logger contracts.Logger) (contracts.ConfigProvider, *config.VolumeConfigWatcher, error) {
	// Log that we're starting config initialization with the passed logger
	logger.Info("=== STARTING initializeConfigWithWatcher ===")
	logger.Info("Starting configuration initialization with volume watcher",
		"function", "initializeConfigWithWatcher")

	// Determine environment
	env := os.Getenv("ENVIRONMENT")
	if env == "" {
		env = os.Getenv("ENV")
		if env == "" {
			env = "dev"
		}
	}

	logger.Infow("Environment determined, loading configuration", "environment", env)

	// Prepare secret resolver options if AWS secrets are enabled
	var secretResolverOptions *config.SecretResolverOptions

	logger.Info("Loading basic config for secrets")
	basicConfig, err := loadBasicConfigForSecrets(env)
	var enableSecrets bool
	var awsRegion, secretPrefix string

	if err == nil && basicConfig != nil {
		enableSecrets = basicConfig.GetBool("aws.secrets.enabled")
		awsRegion = basicConfig.GetString("aws.secrets.region")
		secretPrefix = basicConfig.GetString("aws.secrets.prefix")

		logger.Infow("AWS secrets config loaded from files", "enabled", enableSecrets, "region", awsRegion, "prefixSet", secretPrefix != "")
	} else {
		if err != nil {
			logger.Errorw("Error loading basic config for secrets", "error", err)
		}
	}

	if !enableSecrets {
		enableSecrets = os.Getenv("ENABLE_AWS_SECRETS") == "true"
	}
	if awsRegion == "" {
		awsRegion = os.Getenv("AWS_REGION")
	}
	if secretPrefix == "" {
		secretPrefix = os.Getenv("SECRET_MANAGER_PREFIX")
	}

	logger.Infow("Final AWS secrets config", "enabled", enableSecrets, "region", awsRegion, "prefixSet", secretPrefix != "")

	if enableSecrets {
		secretResolverOptions = &config.SecretResolverOptions{
			Region:       awsRegion,
			SecretPrefix: secretPrefix,
			Logger:       logger,
		}
		logger.Info("AWS Secret resolution enabled")
	} else {
		logger.Info("AWS Secret resolution disabled")
	}

	watcherOptions := config.VolumeConfigWatcherOptions{
		ServiceName:           "decision-manager",
		Environment:           env,
		ConfigPath:            "/etc/config",
		Logger:                logger,
		SecretResolverOptions: secretResolverOptions,
	}

	logger.Info("Initializing volume config watcher")
	watcher, err := config.NewVolumeConfigWatcher(watcherOptions)
	if err != nil {
		logger.Errorw("Volume config watcher initialization failed, falling back to file-based config", "error", err)

		configProvider, fallbackErr := initializeFallbackConfig(env)
		if fallbackErr != nil {
			logger.Errorw("Fallback config also failed", "error", fallbackErr)
			return nil, nil, fmt.Errorf("both volume and file-based configuration failed: Volume error: %v, Fallback error: %v", err, fallbackErr)
		}

		logger.Info("Fallback file-based configuration loaded successfully")
		return configProvider, nil, nil
	}

	configProvider := watcher.GetConfig()
	if configProvider == nil {
		return nil, nil, fmt.Errorf("failed to get initial configuration from volume watcher")
	}

	allSettings := configProvider.GetAll()
	logger.Infow("Configuration loaded", "totalKeys", len(allSettings))

	// Verify critical config keys are present (without logging sensitive values)
	dbKeys := []string{"database.host", "database.port", "database.username", "database.password", "database.name"}
	for _, key := range dbKeys {
		value := configProvider.GetString(key)
		logger.Infow("Config key check", "key", key, "present", value != "")
	}

	logger.Info("Volume-based configuration loaded successfully")
	return configProvider, watcher, nil
}

// Helper function to get configuration keys for debugging
func getConfigKeys(settings map[string]interface{}) []string {
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	return keys
}

// getNamespaceForEnvironment returns the appropriate namespace for the environment
func getNamespaceForEnvironment(env string) string {
	namespaceMap := map[string]string{
		"dev":         "development",
		"development": "development",
		"staging":     "staging",
		"stage":       "staging",
		"production":  "production",
		"prod":        "production",
		"test":        "testing",
		"testing":     "testing",
	}

	if namespace, exists := namespaceMap[env]; exists {
		return namespace
	}

	// Default to environment name if not mapped
	return env
}

// loadBasicConfigForSecrets loads basic configuration to read AWS secrets settings.
// Uses fmt for logging because the structured logger may not be fully configured yet.
func loadBasicConfigForSecrets(env string) (contracts.ConfigProvider, error) {
	v := viper.New()
	v.SetEnvPrefix("DECISION_MANAGER")
	v.AutomaticEnv()

	configFound := false

	jsonConfigPaths := []string{
		fmt.Sprintf("./configs/config_%s.json", env),
		"./configs/config.json",
	}

	for _, path := range jsonConfigPaths {
		if _, err := os.Stat(path); err == nil {
			v.SetConfigFile(path)
			if err := v.ReadInConfig(); err == nil {
				configFound = true
				fmt.Printf("[init] JSON config loaded: %s\n", path)
				break
			} else {
				fmt.Printf("[init] Error reading JSON config %s: %v\n", path, err)
			}
		}
	}

	propertiesPaths := []string{
		fmt.Sprintf("../configurations/decision-manager/decision-manager_%s.properties", env),
		fmt.Sprintf("./configs/decision-manager_%s.properties", env),
	}

	for _, path := range propertiesPaths {
		if _, err := os.Stat(path); err == nil {
			propViper := viper.New()
			propViper.SetConfigFile(path)
			propViper.SetConfigType("properties")

			if err := propViper.ReadInConfig(); err == nil {
				for key, value := range propViper.AllSettings() {
					v.Set(key, value)
				}
				configFound = true
				fmt.Printf("[init] Properties config merged: %s\n", path)
			} else {
				fmt.Printf("[init] Error reading properties config %s: %v\n", path, err)
			}
		}
	}

	if !configFound {
		return nil, fmt.Errorf("no configuration file found")
	}

	allSettings := v.AllSettings()
	fmt.Printf("[init] Basic config for secrets loaded, totalKeys=%d\n", len(allSettings))

	return config.NewViperConfigFromExisting(v), nil
}

// initializeFallbackConfig initializes file-based configuration as fallback.
// Uses fmt for early-startup messages when the structured logger may not be ready.
func initializeFallbackConfig(env string) (contracts.ConfigProvider, error) {
	v := viper.New()
	v.SetEnvPrefix("DECISION_MANAGER")
	v.AutomaticEnv()

	configFound := false

	jsonConfigPaths := []string{
		fmt.Sprintf("./configs/config_%s.json", env),
		"./configs/config.json",
	}

	for _, path := range jsonConfigPaths {
		if _, err := os.Stat(path); err == nil {
			v.SetConfigFile(path)
			if err := v.ReadInConfig(); err != nil {
				fmt.Printf("[init] Error reading JSON config %s: %v\n", path, err)
			} else {
				configFound = true
				fmt.Printf("[init] JSON config loaded: %s\n", path)
				break
			}
		}
	}

	propertiesPaths := []string{
		fmt.Sprintf("../configurations/decision-manager/decision-manager_%s.properties", env),
		fmt.Sprintf("./configs/decision-manager_%s.properties", env),
	}

	for _, path := range propertiesPaths {
		if _, err := os.Stat(path); err == nil {
			propViper := viper.New()
			propViper.SetConfigFile(path)
			propViper.SetConfigType("properties")

			if err := propViper.ReadInConfig(); err != nil {
				fmt.Printf("[init] Error reading properties config %s: %v\n", path, err)
			} else {
				for key, value := range propViper.AllSettings() {
					v.Set(key, value)
				}
				configFound = true
				fmt.Printf("[init] Properties config merged: %s\n", path)
			}
		}
	}

	if !configFound {
		return nil, fmt.Errorf("no configuration file found in any of the paths")
	}

	configProvider := config.NewViperConfigFromExisting(v)
	allSettings := v.AllSettings()
	fmt.Printf("[init] Fallback config loaded, totalKeys=%d\n", len(allSettings))

	// Verify critical keys present without logging values
	dbKeys := []string{"database.host", "database.port", "database.username", "database.password", "database.name"}
	for _, key := range dbKeys {
		_, exists := allSettings[key]
		fmt.Printf("[init] Config key %s: present=%t\n", key, exists)
	}

	return configProvider, nil
}

// initializeLogger initializes the logger
func initializeLogger(configProvider contracts.ConfigProvider) (contracts.Logger, error) {
	// Default logger configuration
	logConfig := logger.Config{
		Level:       logger.LogLevel("info"),
		Development: false,
		Encoding:    "json",
		// Note: OutputPaths and ErrorOutputPaths will default to ["stdout"] and ["stderr"] in NewZapLogger
	}

	// Override with configuration if available
	if configProvider != nil {
		if level := configProvider.GetString("log.level"); level != "" {
			logConfig.Level = logger.LogLevel(level)
		}
		if format := configProvider.GetString("log.format"); format != "" {
			logConfig.Encoding = format
		}
		if configProvider.GetBool("log.development") {
			logConfig.Development = true
		}
	}

	return logger.NewZapLogger(logConfig)
}

// initializeDecisionManagerCacheConfig initializes the Decision Manager-specific cache configuration
func initializeDecisionManagerCacheConfig(configProvider contracts.ConfigProvider) (*DecisionManagerCacheConfig, error) {
	if configProvider == nil {
		return nil, fmt.Errorf("configuration provider required for Decision Manager cache configuration initialization")
	}

	// Default Decision Manager cache configuration initialization
	decisionManagerCacheConfig := &DecisionManagerCacheConfig{
		Host:                       "localhost",
		Port:                       6379,
		Password:                   "",
		Database:                   0,
		PoolSize:                   10,
		MinIdleConns:               5,
		MaxRetries:                 3,
		DialTimeout:                5 * time.Second,
		ReadTimeout:                3 * time.Second,
		WriteTimeout:               3 * time.Second,
		PoolTimeout:                4 * time.Second,
		DefaultTTL:                 360 * time.Minute,
		PartnerServiceMappingTTL:   360 * time.Minute,
		QueryObjectTTL:             360 * time.Minute,
		ServiceSFDCFieldMappingTTL: 360 * time.Minute,
		Enabled:                    true,
		Compression:                false,
	}

	// Override with configuration values
	if host := configProvider.GetString("cache.host"); host != "" {
		decisionManagerCacheConfig.Host = host
	}
	if port := configProvider.GetInt("cache.port"); port > 0 {
		decisionManagerCacheConfig.Port = port
	}
	if password := configProvider.GetString("cache.password"); password != "" {
		decisionManagerCacheConfig.Password = password
	}
	if db := configProvider.GetInt("cache.database"); db >= 0 {
		decisionManagerCacheConfig.Database = db
	}
	if poolSize := configProvider.GetInt("cache.pool_size"); poolSize > 0 {
		decisionManagerCacheConfig.PoolSize = poolSize
	}
	if minIdleConns := configProvider.GetInt("cache.min_idle_conns"); minIdleConns > 0 {
		decisionManagerCacheConfig.MinIdleConns = minIdleConns
	}
	if maxRetries := configProvider.GetInt("cache.max_retries"); maxRetries > 0 {
		decisionManagerCacheConfig.MaxRetries = maxRetries
	}
	if dialTimeout := configProvider.GetString("cache.dial_timeout"); dialTimeout != "" {
		if duration, err := time.ParseDuration(dialTimeout); err == nil {
			decisionManagerCacheConfig.DialTimeout = duration
		}
	}
	if readTimeout := configProvider.GetString("cache.read_timeout"); readTimeout != "" {
		if duration, err := time.ParseDuration(readTimeout); err == nil {
			decisionManagerCacheConfig.ReadTimeout = duration
		}
	}
	if writeTimeout := configProvider.GetString("cache.write_timeout"); writeTimeout != "" {
		if duration, err := time.ParseDuration(writeTimeout); err == nil {
			decisionManagerCacheConfig.WriteTimeout = duration
		}
	}
	if poolTimeout := configProvider.GetString("cache.pool_timeout"); poolTimeout != "" {
		if duration, err := time.ParseDuration(poolTimeout); err == nil {
			decisionManagerCacheConfig.PoolTimeout = duration
		}
	}
	if defaultTTL := configProvider.GetString("cache.default_ttl"); defaultTTL != "" {
		if duration, err := time.ParseDuration(defaultTTL); err == nil {
			decisionManagerCacheConfig.DefaultTTL = duration
		}
	}
	if partnerServiceMappingTTL := configProvider.GetString("cache.partner_service_mapping_ttl"); partnerServiceMappingTTL != "" {
		if duration, err := time.ParseDuration(partnerServiceMappingTTL); err == nil {
			decisionManagerCacheConfig.PartnerServiceMappingTTL = duration
		}
	}
	if queryObjectTTL := configProvider.GetString("cache.query_object_ttl"); queryObjectTTL != "" {
		if duration, err := time.ParseDuration(queryObjectTTL); err == nil {
			decisionManagerCacheConfig.QueryObjectTTL = duration
		}
	}
	if serviceSFDCFieldMappingTTL := configProvider.GetString("cache.service_sfdc_field_mapping_ttl"); serviceSFDCFieldMappingTTL != "" {
		if duration, err := time.ParseDuration(serviceSFDCFieldMappingTTL); err == nil {
			decisionManagerCacheConfig.ServiceSFDCFieldMappingTTL = duration
		}
	}
	if enabled := configProvider.GetBool("cache.enabled"); !enabled {
		decisionManagerCacheConfig.Enabled = false
	}
	if compression := configProvider.GetBool("cache.compression"); compression {
		decisionManagerCacheConfig.Compression = true
	}

	return decisionManagerCacheConfig, nil
}

// initializeCache initializes the cache provider using the new cache configuration structure
func initializeCache(configProvider contracts.ConfigProvider, logger contracts.Logger) (contracts.CacheProvider, error) {
	if configProvider == nil {
		return nil, fmt.Errorf("configuration provider required for cache initialization")
	}

	// Check if cache is disabled
	if enabled := configProvider.GetBool("cache.enabled"); !enabled {
		if logger != nil {
			logger.Infow("Cache is disabled by configuration", "cache.enabled", false)
		}
		return nil, fmt.Errorf("cache is disabled by configuration")
	}

	// Default cache configuration for common-modules compatibility
	cacheConfig := &contracts.CacheConfig{
		Host:           "localhost",
		Port:           6379,
		Password:       "",
		Database:       0,
		Enabled:        true,
		ClusterEnabled: true,
	}

	// Override with configuration - use the same keys as Decision Manager cache config
	if host := configProvider.GetString("cache.host"); host != "" {
		cacheConfig.Host = host
	}
	if port := configProvider.GetInt("cache.port"); port > 0 {
		cacheConfig.Port = port
	}
	if password := configProvider.GetString("cache.password"); password != "" {
		cacheConfig.Password = password
	}
	if db := configProvider.GetInt("cache.database"); db >= 0 {
		cacheConfig.Database = db
	}
	if configProvider.IsSet("cache.tls_enabled") {
		cacheConfig.TLSEnabled = configProvider.GetBool("cache.tls_enabled")
	} else {
		cacheConfig.TLSEnabled = true // default true for MemoryDB
	}
	if configProvider.IsSet("cache.tls_insecure_skip_verify") {
		cacheConfig.TLSInsecureSkipVerify = configProvider.GetBool("cache.tls_insecure_skip_verify")
	} else {
		cacheConfig.TLSInsecureSkipVerify = true // default true for MemoryDB (match redis-cli --insecure)
	}
	if username := configProvider.GetString("cache.username"); username != "" {
		cacheConfig.Username = username
	}
	if configProvider.IsSet("cache.cluster_enabled") {
		cacheConfig.ClusterEnabled = configProvider.GetBool("cache.cluster_enabled")
	}

	// Log the configuration being used (without sensitive data)
	if logger != nil {
		logger.Infow("Initializing Redis cache with configuration",
			"host", cacheConfig.Host,
			"port", cacheConfig.Port,
			"database", cacheConfig.Database,
			"hasPassword", cacheConfig.Password != "",
			"tlsEnabled", cacheConfig.TLSEnabled,
			"clusterEnabled", cacheConfig.ClusterEnabled,
			"hasUsername", cacheConfig.Username != "",
			"enabled", cacheConfig.Enabled)
	}

	// Attempt to create Redis cache with enhanced error handling
	redisCache, err := cache.NewRedisCache(cacheConfig)
	if err != nil {
		// Provide more detailed error information
		if logger != nil {
			logger.Errorw("Failed to initialize Redis cache",
				"error", err,
				"host", cacheConfig.Host,
				"port", cacheConfig.Port,
				"database", cacheConfig.Database)
		}
		return nil, fmt.Errorf("failed to initialize Redis cache at %s:%d: %w", cacheConfig.Host, cacheConfig.Port, err)
	}

	if logger != nil {
		logger.Infow("Redis cache initialized successfully",
			"host", cacheConfig.Host,
			"port", cacheConfig.Port,
			"database", cacheConfig.Database)
	}

	return redisCache, nil
}

// initializeAPIClient initializes the API client (DM stays on non-pooled client until Phase 2)
func initializeAPIClient(configProvider contracts.ConfigProvider, logger contracts.Logger, utilityProvider contracts.UtilityProvider) (contracts.APIClient, error) {
	clientConfig := client.ClientConfig{
		DefaultTimeout:    30,
		DefaultRetryCount: 1,
		DefaultHeaders:    make(map[string]string),
		UseConnectionPool: false,
	}

	if configProvider != nil {
		if timeout := configProvider.GetInt("RestExecuteTimeoutInSeconds"); timeout > 0 {
			clientConfig.DefaultTimeout = timeout
		}
		if retryCount := configProvider.GetInt("http.retry.count"); retryCount > 0 {
			clientConfig.DefaultRetryCount = retryCount
		}
	}

	clientConfig.DefaultHeaders["User-Agent"] = "DecisionManager/1.0"

	return client.NewAPIClient(clientConfig, logger, utilityProvider), nil
}

// initializeShutdownManager initializes the shutdown manager
func initializeShutdownManager(configProvider contracts.ConfigProvider, logger contracts.Logger) (*shutdown.Manager, error) {
	// Default shutdown configuration
	shutdownConfig := shutdown.Config{
		ShutdownTimeout: 60 * time.Second,
		Logger:          logger,
	}

	// Override with configuration if available
	if configProvider != nil {
		if timeout := configProvider.GetInt("server.shutdown.timeout"); timeout > 0 {
			shutdownConfig.ShutdownTimeout = time.Duration(timeout) * time.Second
		}
	}

	return shutdown.NewManager(shutdownConfig), nil
}

// Shutdown gracefully shuts down all initialized components
func Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	initMutex.Lock()
	if isShuttingDown {
		initMutex.Unlock()
		return nil
	}
	if !isInitialized || GlobalModules == nil {
		initMutex.Unlock()
		return nil
	}
	isShuttingDown = true
	modules := GlobalModules
	logger := modules.Logger
	shutdownManager := modules.ShutdownManager
	cacheProvider := modules.Cache
	configWatcher := modules.ConfigWatcher
	initMutex.Unlock()

	var errors []string
	shutdownSucceeded := false
	defer func() {
		initMutex.Lock()
		defer initMutex.Unlock()
		if shutdownSucceeded {
			isInitialized = false
			if GlobalModules == modules {
				GlobalModules = nil
			}
		}
		isShuttingDown = false
	}()

	// Broadcast shutdown to long-running goroutines before waiting.
	appShutdownSignalCloseOnce.Do(func() {
		close(appShutdownSignal)
	})

	// Stop config watcher early so it doesn't continue emitting callbacks during shutdown.
	if configWatcher != nil {
		configWatcher.Stop()
	}

	// Shutdown in reverse order of initialization
	if shutdownManager != nil {
		if err := shutdownManager.ShutdownWithContext(ctx); err != nil {
			errors = append(errors, fmt.Sprintf("shutdown manager: %v", err))
		}
	}

	// Drain in-flight request goroutines BEFORE closing the cache client.
	// If cache is closed first, goroutines still running will get "redis: client is closed".
	if err := WaitForTrackedGoroutines(ctx); err != nil {
		errors = append(errors, fmt.Sprintf("tracked goroutines: %v", err))
	}

	if cacheProvider != nil {
		// Add panic recovery for cache close operation to prevent shutdown crashes
		func() {
			defer func() {
				if r := recover(); r != nil {
					errors = append(errors, fmt.Sprintf("cache close panic: %v", r))
					if logger != nil {
						logger.Errorw("Cache close operation panicked during shutdown", "panic", r)
					}
				}
			}()
			if err := cacheProvider.Close(); err != nil {
				if isTLSCloseNotifyError(err) {
					// TLS close-notify send failed but the connection was already closed — non-fatal.
					if logger != nil {
						logger.Infow("Cache Redis connection closed (TLS close-notify timed out, connection was already closed)", "detail", err.Error())
					}
				} else {
					errors = append(errors, fmt.Sprintf("cache: %v", err))
				}
			}
		}()
	}

	if len(errors) > 0 {
		return fmt.Errorf("shutdown errors: %s", strings.Join(errors, ", "))
	}

	shutdownSucceeded = true
	fmt.Println("🛑 Common Modules shutdown completed")

	return nil
}

// Helper functions to get initialized components
func GetLogger(ctx ...context.Context) contracts.Logger {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.Logger == nil {
		panic("Logger not initialized. Call InitializeCommonModules() with EnableLogger=true first")
	}

	if len(ctx) > 0 && ctx[0] != nil {
		if reqLogger := cmcontext.GetRequestLoggerFromContext(ctx[0]); reqLogger != nil {
			return reqLogger
		}
		return GlobalModules.Logger.WithContext(ctx[0])
	}

	return GlobalModules.Logger
}

func GetConfig() contracts.ConfigProvider {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.Config == nil {
		panic("Config not initialized. Call InitializeCommonModules() with EnableConfig=true first")
	}
	return GlobalModules.Config
}

func GetCache() contracts.CacheProvider {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.Cache == nil {
		// Return a no-op cache implementation instead of panicking
		return &NoOpCacheProvider{}
	}
	return GlobalModules.Cache
}

func GetAPIClient() contracts.APIClient {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.APIClient == nil {
		panic("APIClient not initialized. Call InitializeCommonModules() with EnableAPIClient=true first")
	}
	return GlobalModules.APIClient
}

func GetUtilityProvider() contracts.UtilityProvider {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.UtilityProvider == nil {
		panic("UtilityProvider not initialized. Call InitializeCommonModules() with EnableUtilityProvider=true first")
	}
	return GlobalModules.UtilityProvider
}

func GetShutdownManager() *shutdown.Manager {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.ShutdownManager == nil {
		panic("ShutdownManager not initialized. Call InitializeCommonModules() with EnableShutdownManager=true first")
	}
	return GlobalModules.ShutdownManager
}

func GetCacheKeyBuilder() contracts.CacheKeyBuilder {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.CacheKeyBuilder == nil {
		panic("CacheKeyBuilder not initialized. Call InitializeCommonModules() with EnableCacheKeyBuilder=true first")
	}
	return GlobalModules.CacheKeyBuilder
}

func GetDecisionManagerCacheConfig() *DecisionManagerCacheConfig {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.DecisionManagerCacheConfig == nil {
		panic("DecisionManagerCacheConfig not initialized. Call InitializeCommonModules() with EnableCache=true first")
	}
	return GlobalModules.DecisionManagerCacheConfig
}

// InitTriggerDecisionLimiter initializes the trigger-decision concurrency limiter from config (server.max_concurrent_trigger_decisions).
// Call once after InitializeCommonModules from main. When config is 0 or unset, no cap is applied.
func InitTriggerDecisionLimiter() {
	triggerDecisionLimiterOnce.Do(func() {
		cap := GetConfigInt(constants.ServerMaxConcurrentTriggerDecisions, 0)
		if cap > 0 {
			triggerDecisionLimiter = make(chan struct{}, cap)
			if GlobalModules != nil && GlobalModules.Logger != nil {
				GlobalModules.Logger.Infow("Trigger decision limiter initialized", "max_concurrent_trigger_decisions", cap)
			}
		}
	})
}

// TryAcquireTriggerDecision acquires a slot for a trigger-decision request. Returns true if acquired or when limiter is disabled.
func TryAcquireTriggerDecision() bool {
	if triggerDecisionLimiter == nil {
		return true
	}
	select {
	case triggerDecisionLimiter <- struct{}{}:
		return true
	default:
		return false
	}
}

// ReleaseTriggerDecision releases a slot; must only be called after a successful TryAcquireTriggerDecision.
func ReleaseTriggerDecision() {
	if triggerDecisionLimiter != nil {
		<-triggerDecisionLimiter
	}
}

// Helper functions to check if components are available
func IsLoggerAvailable() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return GlobalModules != nil && GlobalModules.Logger != nil
}

func IsConfigAvailable() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return GlobalModules != nil && GlobalModules.Config != nil
}

func IsCacheAvailable() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return GlobalModules != nil && GlobalModules.Cache != nil
}

func IsAPIClientAvailable() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return GlobalModules != nil && GlobalModules.APIClient != nil
}

func IsUtilityProviderAvailable() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return GlobalModules != nil && GlobalModules.UtilityProvider != nil
}

func IsShutdownManagerAvailable() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return GlobalModules != nil && GlobalModules.ShutdownManager != nil
}

func IsCacheKeyBuilderAvailable() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return GlobalModules != nil && GlobalModules.CacheKeyBuilder != nil
}

func IsDecisionManagerCacheConfigAvailable() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return GlobalModules != nil && GlobalModules.DecisionManagerCacheConfig != nil
}

// GetCacheStatus returns detailed cache status information for debugging
func GetCacheStatus() map[string]interface{} {
	initMutex.RLock()
	defer initMutex.RUnlock()

	status := map[string]interface{}{
		"initialized": false,
		"available":   false,
		"error":       nil,
	}

	if GlobalModules == nil {
		status["error"] = "GlobalModules is nil"
		return status
	}

	status["initialized"] = true

	if GlobalModules.Cache != nil {
		status["available"] = true
	} else {
		status["error"] = "Cache is nil (likely failed to initialize)"
	}

	return status
}

// IsInitialized returns whether common modules have been initialized
func IsInitialized() bool {
	initMutex.RLock()
	defer initMutex.RUnlock()
	return isInitialized
}

// GetInitializationStatus returns detailed status of all components.
// Uses direct field checks instead of calling the public Is*Available()
// methods to avoid recursive RLock acquisition (which can deadlock if a
// writer is pending between the outer and inner RLock).
func GetInitializationStatus() map[string]bool {
	initMutex.RLock()
	defer initMutex.RUnlock()

	status := make(map[string]bool)
	status["initialized"] = isInitialized
	status["logger"] = GlobalModules != nil && GlobalModules.Logger != nil
	status["config"] = GlobalModules != nil && GlobalModules.Config != nil
	status["cache"] = GlobalModules != nil && GlobalModules.Cache != nil
	status["apiClient"] = GlobalModules != nil && GlobalModules.APIClient != nil
	status["utilityProvider"] = GlobalModules != nil && GlobalModules.UtilityProvider != nil
	status["shutdownManager"] = GlobalModules != nil && GlobalModules.ShutdownManager != nil
	status["cacheKeyBuilder"] = GlobalModules != nil && GlobalModules.CacheKeyBuilder != nil
	status["decisionManagerCacheConfig"] = GlobalModules != nil && GlobalModules.DecisionManagerCacheConfig != nil

	return status
}

// GetConfigValue provides a centralized way to get configuration values with fallbacks
func GetConfigValue(key string, defaultValue interface{}) interface{} {
	initMutex.RLock()
	defer initMutex.RUnlock()

	if GlobalModules == nil || GlobalModules.Config == nil {
		return defaultValue
	}

	config := GlobalModules.Config

	// Check if the exact key exists first
	if config.IsSet(key) {
		switch defaultValue.(type) {
		case string:
			return config.GetString(key)
		case int:
			return config.GetInt(key)
		case bool:
			return config.GetBool(key)
		case float64:
			return config.GetFloat64(key)
		case []string:
			return config.GetStringSlice(key)
		default:
			return config.GetString(key)
		}
	}

	return defaultValue
}

// GetConfigString returns a string configuration value with fallback
func GetConfigString(key string, defaultValue ...string) string {
	defaultVal := ""
	if len(defaultValue) > 0 {
		defaultVal = defaultValue[0]
	}
	return GetConfigValue(key, defaultVal).(string)
}

// GetConfigInt returns an int configuration value with fallback
func GetConfigInt(key string, defaultValue ...int) int {
	defaultVal := 0
	if len(defaultValue) > 0 {
		defaultVal = defaultValue[0]
	}
	return GetConfigValue(key, defaultVal).(int)
}

// GetConfigBool returns a bool configuration value with fallback
func GetConfigBool(key string, defaultValue ...bool) bool {
	defaultVal := false
	if len(defaultValue) > 0 {
		defaultVal = defaultValue[0]
	}
	return GetConfigValue(key, defaultVal).(bool)
}

// GetConfigStringSlice returns a string slice configuration value with fallback
func GetConfigStringSlice(key string, defaultValue ...[]string) []string {
	var defaultVal []string
	if len(defaultValue) > 0 {
		defaultVal = defaultValue[0]
	}
	return GetConfigValue(key, defaultVal).([]string)
}

// isTLSCloseNotifyError reports whether the error is the benign TLS close-notify
// timeout that occurs when a Redis connection is closed during shutdown.
// The Go TLS stack still reports this as an error even though the underlying
// TCP connection was already terminated ("but connection was closed anyway").
func isTLSCloseNotifyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "tls: failed to send closeNotify alert")
}
