// internal/app/db/abstraction/generic_provider.go
package db

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/pressly/goose"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// GenericProvider implements the DatabaseProvider interface
type GenericProvider struct {
	dialectName string
	dsn         func(*DatabaseConfig) string
	dialector   func(*sql.DB) gorm.Dialector
	driverName  string
}

// NewGenericProvider creates a provider with the given configuration
func NewGenericProvider(
	dialectName string,
	dsnBuilder func(*DatabaseConfig) string,
	dialectorBuilder func(*sql.DB) gorm.Dialector,
	driverName string,
) DatabaseProvider {
	return &GenericProvider{
		dialectName: dialectName,
		dsn:         dsnBuilder,
		dialector:   dialectorBuilder,
		driverName:  driverName,
	}
}

// Connect establishes a database connection
func (p *GenericProvider) Connect(config *DatabaseConfig) (*gorm.DB, error) {
	// Build connection string using the provided DSN builder
	dsn := p.dsn(config)

	// Log connection info for debugging
	fmt.Printf("DEBUG - Connecting to %s database: %s\n", p.dialectName,
		config.Host)

	// Open a standard sql.DB connection
	sqlDB, err := sql.Open(p.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool
	sqlDB.SetMaxIdleConns(config.MaxIdleConnections)
	sqlDB.SetMaxOpenConns(config.MaxOpenConnections)
	sqlDB.SetConnMaxLifetime(time.Hour * time.Duration(config.ConnMaxLifetimeInHours))

	// Create GORM connection using the dialector
	db, err := gorm.Open(p.dialector(sqlDB), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to initialize GORM: %w", err)
	}

	return db, nil
}

// RunGooseMigrations runs SQL migrations
func (p *GenericProvider) RunGooseMigrations(db *gorm.DB, migrationsDir string) error {
	// Get underlying SQL DB
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get SQL DB: %w", err)
	}

	// Check migrations directory exists
	if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
		return fmt.Errorf("migrations directory %s does not exist", migrationsDir)
	}

	// Run Goose migrations
	if err := goose.SetDialect(p.dialectName); err != nil {
		return fmt.Errorf("failed to set dialect: %w", err)
	}

	if err := goose.Up(sqlDB, migrationsDir); err != nil {
		return fmt.Errorf("goose migration failed: %w", err)
	}

	return nil
}

// GetDialectName returns the dialect name
func (p *GenericProvider) GetDialectName() string {
	return p.dialectName
}

// RunGormAutoMigrations runs GORM auto-migrations
func (p *GenericProvider) RunGormAutoMigrations(db *gorm.DB, models ...interface{}) error {
	if err := db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto-migration failed: %w", err)
	}
	return nil
}
