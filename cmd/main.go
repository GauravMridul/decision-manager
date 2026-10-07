package main

//This branch is for logging secrets resolution in case issue of secrets or config resolution

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	_ "decision-manager/docs"
	"decision-manager/internal/app/api/router"
	"decision-manager/internal/app/constants"
	"decision-manager/internal/app/db"
	"decision-manager/internal/app/db/repository"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/middleware/auth"
	"decision-manager/pkg/client"

	"github.com/gin-gonic/gin"
)

func main() {
	// Comprehensive panic recovery for the entire application lifecycle
	defer func() {
		if r := recover(); r != nil {
			// Get stack trace for debugging
			stackTrace := debug.Stack()

			// Try to use the common logger if available, otherwise fallback to standard log
			if commoninit.IsLoggerAvailable() {
				logger := commoninit.GetLogger()
				logger.Errorw("🚨 Critical application panic - initiating emergency shutdown",
					"panic", r,
					"stackTrace", string(stackTrace))
			} else {
				log.Printf("🚨 Critical application panic during early initialization: %v\nStack trace:\n%s", r, stackTrace)
			}

			// Attempt graceful shutdown of any initialized components
			performEmergencyShutdown()

			// Exit with error code
			logFatalShutdown("main_panic_recovered", fmt.Errorf("%v", r), "main")
			os.Exit(1)
		}
	}()

	// Initialize and run the application
	if err := runApplication(); err != nil {
		log.Printf("❌ Application failed to start: %v", err)
		performEmergencyShutdown()
		logFatalShutdown("application_startup_failed", err, "runApplication")
		os.Exit(1)
	}
}

func logFatalShutdown(reason string, err error, component string) {
	if commoninit.IsLoggerAvailable() {
		logger := commoninit.GetLogger()
		logger.Errorw("Fatal shutdown initiated",
			"reason", reason,
			"component", component,
			"error", err)
		return
	}
	log.Printf("FATAL SHUTDOWN reason=%s component=%s error=%v", reason, component, err)
}

// runApplication contains the main application logic with proper error handling
func runApplication() error {
	// Initialize Common Modules with all components
	fmt.Println("🚀 Initializing Common Modules for Decision Manager...")

	// Initialize using the default configuration
	if err := commoninit.InitializeCommonModules(commoninit.DefaultCommonModulesConfig()); err != nil {
		return fmt.Errorf("failed to initialize common modules: %v", err)
	}

	logger := commoninit.GetLogger()
	logger.Info("🚀 Decision Manager Microservice starting up...")

	// Log initialization status
	status := commoninit.GetInitializationStatus()
	logger.Infow("Common modules initialized successfully",
		"config_available", status["config"],
		"cache_available", status["cache"],
		"api_client_available", status["apiClient"])

	commoninit.InitTriggerDecisionLimiter()

	// Initialize all components with comprehensive error handling
	if err := initializeAllComponents(logger); err != nil {
		return fmt.Errorf("component initialization failed: %v", err)
	}

	// Start the HTTP server with error handling
	httpServer, err := startHTTPServer(logger)
	if err != nil {
		return fmt.Errorf("failed to start HTTP server: %v", err)
	}

	// Set up graceful shutdown handling
	return handleGracefulShutdown(logger, httpServer)
}

// initializeAllComponents initializes all application components with error recovery
func initializeAllComponents(logger interface{}) error {
	log := logger.(interface {
		Info(...interface{})
		Infow(string, ...interface{})
		Errorw(string, ...interface{})
	})

	// Database initialization with error recovery
	log.Info("🗄️ Initializing databases...")
	if err := safeExecute("Database initialization", func() error {
		return initializeDatabases(logger)
	}, log); err != nil {
		return err
	}
	log.Info("✅ All databases initialized successfully")

	// Authentication middleware initialization
	log.Info("🔐 Initializing authentication middleware...")
	if err := safeExecute("Authentication middleware initialization", func() error {
		auth.Init()
		return nil
	}, log); err != nil {
		return err
	}
	log.Info("✅ Authentication middleware initialized successfully")

	return nil
}

// startHTTPServer initializes and starts the HTTP server
func startHTTPServer(logger interface{}) (*http.Server, error) {
	log := logger.(interface {
		Info(...interface{})
		Infow(string, ...interface{})
		Errorw(string, ...interface{})
	})

	// Initialize router with error recovery
	var ginRouter *gin.Engine
	if err := safeExecute("Router initialization", func() error {
		ginRouter = router.NewRouter()
		return nil
	}, log); err != nil {
		return nil, err
	}

	// Get server configuration
	port := commoninit.GetConfigString("server.port", ":8080")

	// Ensure port has colon prefix for proper address format
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    port,
		Handler: ginRouter,
	}

	// Start server in goroutine with panic recovery
	serverStarted := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorw("HTTP server panicked during startup", "panic", r, "stackTrace", string(debug.Stack()))
				serverStarted <- fmt.Errorf("server startup panic: %v", r)
			}
		}()

		log.Infow("Starting HTTP server", "port", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorw("HTTP server failed", "error", err)
			serverStarted <- err
		} else {
			serverStarted <- nil
		}
	}()

	// Wait a moment to see if server starts successfully (timer stopped on fast error path to avoid holding timers)
	startupTimer := time.NewTimer(2 * time.Second)
	defer startupTimer.Stop()
	select {
	case err := <-serverStarted:
		if !startupTimer.Stop() {
			select {
			case <-startupTimer.C:
			default:
			}
		}
		if err != nil {
			return nil, err
		}
	case <-startupTimer.C:
		// ListenAndServe still running — treat as successful bind
	}

	log.Info("✅ Server started successfully")
	return httpServer, nil
}

// handleGracefulShutdown sets up signal handling and performs graceful shutdown
func handleGracefulShutdown(logger interface{}, httpServer *http.Server) error {
	log := logger.(interface {
		Info(...interface{})
		Infow(string, ...interface{})
		Errorw(string, ...interface{})
	})

	// Set up graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	shutdownSignal := <-quit

	log.Infow("🛑 Starting graceful shutdown...", "signal", shutdownSignal.String())

	httpTimeoutSec := commoninit.GetConfigInt(constants.ServerShutdownHTTPTimeoutSecondsKey, constants.DefaultShutdownHTTPTimeoutSeconds)
	modulesTimeoutSec := commoninit.GetConfigInt(constants.ServerShutdownModulesTimeoutSecondsKey, constants.DefaultShutdownModulesTimeoutSeconds)

	httpCtx, httpCancel := context.WithTimeout(context.Background(), time.Duration(httpTimeoutSec)*time.Second)
	defer httpCancel()
	if err := httpServer.Shutdown(httpCtx); err != nil {
		log.Errorw("Error during server shutdown", "error", err)
	}

	modulesCtx, modulesCancel := context.WithTimeout(context.Background(), time.Duration(modulesTimeoutSec)*time.Second)
	defer modulesCancel()
	if err := commoninit.Shutdown(modulesCtx); err != nil {
		log.Errorw("Error during common modules shutdown", "error", err)
	}

	log.Info("✅ Graceful shutdown completed")
	return nil
}

// safeExecute wraps any function execution with panic recovery
func safeExecute(operation string, fn func() error, logger interface{}) (err error) {
	log := logger.(interface {
		Errorw(string, ...interface{})
	})

	defer func() {
		if r := recover(); r != nil {
			log.Errorw("Operation panicked",
				"operation", operation,
				"panic", r,
				"stackTrace", string(debug.Stack()))
			err = fmt.Errorf("%s panicked: %v", operation, r)
		}
	}()

	if err = fn(); err != nil {
		log.Errorw("Operation failed", "operation", operation, "error", err)
		return fmt.Errorf("%s failed: %v", operation, err)
	}

	return nil
}

// performEmergencyShutdown attempts to clean up resources during emergency shutdown
func performEmergencyShutdown() {
	defer func() {
		// Even emergency shutdown shouldn't panic
		if r := recover(); r != nil {
			log.Printf("🚨 Emergency shutdown itself panicked: %v", r)
		}
	}()

	log.Printf("🛑 Performing emergency shutdown...")

	// Try to shutdown common modules if they were initialized
	if commoninit.IsLoggerAvailable() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		commoninit.Shutdown(ctx)
	}

	log.Printf("✅ Emergency shutdown completed")
}

// initializeDatabases handles database initialization with proper error handling
func initializeDatabases(logger interface{}) error {
	log := logger.(interface {
		Info(...interface{})
		Errorw(string, ...interface{})
	})

	// Initialize SQL database
	log.Info("📊 Initializing SQL database...")
	if err := db.Init(); err != nil {
		return fmt.Errorf("sql database initialization failed: %v", err)
	}

	// Check if database connection was successful
	log.Info("🔍 Initializing QueryObjectRepository...")
	dbService := db.DBService{}
	sqlDB := dbService.GetDB()

	if sqlDB == nil {
		return fmt.Errorf("SQL database connection is nil - database initialization failed")
	}

	// Get the underlying *sql.DB from GORM with error handling
	sqlConnection, err := sqlDB.DB()
	if err != nil {
		return fmt.Errorf("failed to get SQL connection for QueryObjectRepository: %v", err)
	}

	repository.InitQueryObjectRepository(sqlConnection)
	log.Info("✅ QueryObjectRepository initialized successfully")

	// Initialize Salesforce client with error handling
	log.Info("🔌 Initializing Salesforce client...")
	if err := safeExecute("Salesforce client initialization", func() error {
		salesforceService := &client.SalesforceService{}
		salesforceService.InitializeSalesforceClient(sqlConnection)
		return nil
	}, logger); err != nil {
		return err
	}
	log.Info("✅ Salesforce client initialized successfully")

	// Initialize MongoDB (optional component)
	log.Info("🍃 Initializing MongoDB...")
	if err := safeExecute("MongoDB initialization", func() error {
		ctx := context.Background()
		db.InitMongoDB(ctx)
		return nil
	}, logger); err != nil {
		log.Errorw("MongoDB initialization failed, but continuing", "error", err)
		// MongoDB is optional, so we don't fail the entire startup
	} else {
		log.Info("✅ MongoDB initialized successfully")
	}

	return nil
}
