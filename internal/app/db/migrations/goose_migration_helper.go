package db

import (
	"database/sql"
	commoninit "decision-manager/internal/app/init"
	"fmt"
	"os"

	"github.com/pressly/goose/v3"
)

// RunGooseMigrations runs Goose migrations
func RunGooseMigrations(sqlDB *sql.DB, dialect string, dirPath string) error {
	log := commoninit.GetLogger()

	// Set Goose dialect
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("failed to set dialect: %w", err)
	}

	log.Info("Running Goose migrations path: ", dirPath, " dialect: ", dialect)

	// Run migrations
	if err := goose.Up(sqlDB, dirPath); err != nil {
		return fmt.Errorf("goose migration failed: %w", err)
	}

	return nil
}

// CreateGooseMigration creates a new Goose migration file
func CreateGooseMigration(name string) error {
	log := commoninit.GetLogger()

	// Get migration directory
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	dirPath := fmt.Sprintf("%s/internal/app/db/migrations", workingDir)

	// Create migration file
	log.Info("Creating migration name: ", name, " path: ", dirPath)
	if err := goose.Create(nil, dirPath, name, "sql"); err != nil {
		return fmt.Errorf("failed to create migration: %w", err)
	}

	return nil
}
