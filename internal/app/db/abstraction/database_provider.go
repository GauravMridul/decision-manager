package db

import (
	"gorm.io/gorm"
)

// DatabaseProvider is an interface for database operations
type DatabaseProvider interface {
	// Connect establishes connection to the database
	Connect(config *DatabaseConfig) (*gorm.DB, error)

	// RunGooseMigrations runs Goose file-based migrations
	RunGooseMigrations(db *gorm.DB, migrationsDir string) error

	// AutoMigrate runs GORM auto-migrations for models
	RunGormAutoMigrations(db *gorm.DB, models ...interface{}) error

	// GetDialectName returns the name of the database dialect
	GetDialectName() string
}

// DatabaseConfig contains the database configuration
type DatabaseConfig struct {
	Username     string
	Password     string
	Host         string
	DatabaseName string
	Port         string
	Options      map[string]string

	// Connection pool settings
	MaxIdleConnections     int
	MaxOpenConnections     int
	ConnMaxLifetimeInHours int
}
