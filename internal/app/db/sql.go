// internal/app/db/sql.go
package db

import (
	"decision-manager/internal/app/constants"
	abstraction "decision-manager/internal/app/db/abstraction"
	migrations "decision-manager/internal/app/db/migrations"
	commoninit "decision-manager/internal/app/init"
	"fmt"
	"os"
	"sync"

	// "github.com/spf13/config"
	"gorm.io/gorm"
)

var (
	db          *gorm.DB
	connectOnce sync.Once
)

type DBService struct{}

// Init initializes the database connection
func Init() error {
	log := commoninit.GetLogger()
	config := commoninit.GetConfig()
	if db == nil {
		var initErr error
		connectOnce.Do(func() {
			// Get database configuration
			dbConfig := &abstraction.DatabaseConfig{
				Username:               config.GetString(constants.DatabaseUserName),
				Password:               config.GetString(constants.DatabasePassword),
				Host:                   config.GetString(constants.DatabaseHost),
				DatabaseName:           config.GetString(constants.DatabaseName),
				Port:                   config.GetString(constants.DatabasePort),
				MaxIdleConnections:     config.GetInt(constants.MaxIdleConnections),
				MaxOpenConnections:     config.GetInt(constants.MaxOpenConnections),
				ConnMaxLifetimeInHours: config.GetInt(constants.ConnMaxLifetimeInHours),
				Options:                make(map[string]string),
			}

			// Get the database dialect
			dialect := config.GetString(constants.DatabaseDialect)
			if dialect == "" {
				dialect = "postgres" // Default to postgres for backward compatibility
			}

			// Get the database provider
			provider, err := abstraction.GetDatabaseProvider(dialect)
			if err != nil {
				initErr = fmt.Errorf("failed to get database provider: %w", err)
				log.Errorw("Database initialization failed", "error", initErr)
				return
			}

			// Connect to the database
			var connectErr error
			db, connectErr = provider.Connect(dbConfig)
			if connectErr != nil {
				initErr = fmt.Errorf("failed to connect to %s database: %w", dialect, connectErr)
				log.Errorw("Database connection failed", "dialect", dialect, "error", initErr)
				return
			}

			log.Info("Successfully connected to database dialect: ", provider.GetDialectName())

			// Run auto-migrations (can be enabled/disabled)
			shouldRunAutoMigrations := config.GetBool(constants.ShouldRunAutoMigrations) // Set to false to disable auto-migrations
			if shouldRunAutoMigrations {
				if err := RunGormAutoMigrations(provider, db); err != nil {
					// Non-fatal in development
					if config.GetString(constants.EnvironmentKey) == constants.Production {
						initErr = fmt.Errorf("gorm auto-migrations failed in production: %w", err)
						log.Errorw("Database migration failed", "migrationType", "gorm", "error", initErr)
						return
					}
				}
			} else {
				log.Info("GORM auto-migrations disabled")
			}

			// Run Goose migrations (can be enabled/disabled)
			shouldRunGooseMigrations := config.GetBool(constants.ShouldRunGooseMigrations) // Set to false to disable Goose migrations
			if shouldRunGooseMigrations {
				if err := RunGooseMigrations(provider, db); err != nil {
					// Can be made non-fatal if needed
					if config.GetString(constants.EnvironmentKey) == constants.Production {
						initErr = fmt.Errorf("goose migrations failed in production: %w", err)
						log.Errorw("Database migration failed", "migrationType", "goose", "error", initErr)
						return
					}
				}
			} else {
				log.Info("Goose migrations disabled")
			}

		})
		if initErr != nil {
			return initErr
		}
	}
	if db == nil {
		return fmt.Errorf("database initialization did not produce a connection")
	}
	return nil
}

// RunGormAutoMigrations handles GORM auto migrations
func RunGormAutoMigrations(provider abstraction.DatabaseProvider, db *gorm.DB) error {
	log := commoninit.GetLogger()
	log.Info("Running GORM auto-migrations...")

	if err := provider.RunGormAutoMigrations(db, migrations.GetAllModels()...); err != nil {
		log.Errorw("Auto-migration failed", "error", err.Error())
		return err
	}

	log.Info("Auto-migrations completed successfully")
	return nil
}

func RunGooseMigrations(provider abstraction.DatabaseProvider, db *gorm.DB) error {
	log := commoninit.GetLogger()

	// Get current working directory
	workingDir, err := os.Getwd()
	if err != nil {
		log.Errorw("Failed to get working directory", "error", err.Error())
		return err
	}

	migrationsPath := workingDir + constants.DbMigrationDir

	log.Info("Using migrations path path: ", migrationsPath)

	// Run Goose migrations
	if err := provider.RunGooseMigrations(db, migrationsPath); err != nil {
		log.Errorw("Migration failed", "error", err.Error())
		return err
	}

	log.Info("Database migrations completed successfully")
	return nil
}

// GetDB returns the database connection
func (d DBService) GetDB() *gorm.DB {
	return db
}
