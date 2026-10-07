// internal/app/db/provider_factory.go
package db

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	mysqldriver "gorm.io/driver/mysql"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// GetDatabaseProvider returns a database provider for the specified dialect
func GetDatabaseProvider(dialect string) (DatabaseProvider, error) {
	switch dialect {
	case "postgres":
		return NewGenericProvider(
			"postgres",
			// PostgreSQL DSN builder
			func(config *DatabaseConfig) string {
				return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
					config.Host, config.Port, config.Username, config.Password, config.DatabaseName)
			},
			// PostgreSQL dialector builder
			func(sqlDB *sql.DB) gorm.Dialector {
				return pgdriver.New(pgdriver.Config{Conn: sqlDB})
			},
			"postgres",
		), nil

	case "mysql":
		return NewGenericProvider(
			"mysql",
			// MySQL DSN builder
			func(config *DatabaseConfig) string {
				return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
					config.Username, config.Password, config.Host, config.Port, config.DatabaseName)
			},
			// MySQL dialector builder
			func(sqlDB *sql.DB) gorm.Dialector {
				return mysqldriver.New(mysqldriver.Config{Conn: sqlDB})
			},
			"mysql",
		), nil

	default:
		return nil, fmt.Errorf("unsupported dialect: %s", dialect)
	}
}
