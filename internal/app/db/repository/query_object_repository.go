package repository

import (
	"context"
	"database/sql"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/internal/app/models/decision_manager_models"
	"fmt"
	"strings"
	"sync"
)

// IQueryObjectRepository defines the interface for query object repository operations
type IQueryObjectRepository interface {
	FindByObjectName(ctx context.Context, objectName string) (*decision_manager_models.QueryObjectRelationshipMap, error)
	FindByObjectNames(ctx context.Context, objectNames []string) (map[string]*decision_manager_models.QueryObjectRelationshipMap, error)
}

// Singleton variables for QueryObjectRepository
var (
	instance     *QueryObjectRepository
	instanceOnce sync.Once
)

// QueryObjectRepository implements the IQueryObjectRepository interface
type QueryObjectRepository struct {
	db *sql.DB
}

// InitQueryObjectRepository initializes the global singleton instance of QueryObjectRepository
func InitQueryObjectRepository(db *sql.DB) {
	instanceOnce.Do(func() {
		instance = &QueryObjectRepository{
			db: db,
		}
		commoninit.GetLogger().Info("QueryObjectRepository singleton initialized")
	})
}

// GetQueryObjectRepository returns the global singleton instance of QueryObjectRepository
func GetQueryObjectRepository() IQueryObjectRepository {
	if instance == nil {
		commoninit.GetLogger().Error("QueryObjectRepository not initialized. Make sure to call InitQueryObjectRepository first.")
		panic("QueryObjectRepository not initialized")
	}
	return instance
}

// NewQueryObjectRepository creates a new instance of QueryObjectRepository - only use for tests or specific cases
func NewQueryObjectRepository(db *sql.DB) *QueryObjectRepository {
	return &QueryObjectRepository{
		db: db,
	}
}

// FindByObjectName retrieves a query object relationship map by object name
func (r *QueryObjectRepository) FindByObjectName(ctx context.Context, objectName string) (*decision_manager_models.QueryObjectRelationshipMap, error) {
	log := commoninit.GetLogger(ctx)

	query := `
		SELECT id, query_object, query_relation, additional_fields 
		FROM query_object_relationship_map 
		WHERE query_object = $1 AND is_deleted = false
	`

	var queryObject decision_manager_models.QueryObjectRelationshipMap
	err := r.db.QueryRowContext(ctx, query, objectName).Scan(
		&queryObject.ID,
		&queryObject.QueryObject,
		&queryObject.QueryRelation,
		&queryObject.AdditionalFields,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			log.Warnw("No query object relationship map found", "object", objectName)
			return nil, fmt.Errorf("no query object relationship map found for object: %s", objectName)
		}
		log.Errorw("Failed to query database", "error", err, "object", objectName)
		return nil, err
	}

	return &queryObject, nil
}

// FindByObjectNames retrieves multiple query object relationship maps by object names in a single query
func (r *QueryObjectRepository) FindByObjectNames(ctx context.Context, objectNames []string) (map[string]*decision_manager_models.QueryObjectRelationshipMap, error) {
	log := commoninit.GetLogger(ctx)

	if len(objectNames) == 0 {
		return make(map[string]*decision_manager_models.QueryObjectRelationshipMap), nil
	}

	// Build the query with parameterized placeholders for the IN clause
	queryPlaceholders := make([]string, len(objectNames))
	queryArgs := make([]interface{}, len(objectNames))

	for i, name := range objectNames {
		queryPlaceholders[i] = fmt.Sprintf("$%d", i+1)
		queryArgs[i] = name
	}

	query := fmt.Sprintf(`
		SELECT id, query_object, query_relation, additional_fields 
		FROM query_object_relationship_map 
		WHERE query_object IN (%s) AND is_deleted = false
	`, strings.Join(queryPlaceholders, ","))

	rows, err := r.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		log.Errorw("Failed to query database for multiple objects", "error", err)
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*decision_manager_models.QueryObjectRelationshipMap)

	for rows.Next() {
		var obj decision_manager_models.QueryObjectRelationshipMap
		if err := rows.Scan(
			&obj.ID,
			&obj.QueryObject,
			&obj.QueryRelation,
			&obj.AdditionalFields,
		); err != nil {
			log.Errorw("Failed to scan database row", "error", err)
			return nil, err
		}

		result[obj.QueryObject] = &obj
	}

	if err := rows.Err(); err != nil {
		log.Errorw("Error iterating over rows", "error", err)
		return nil, err
	}

	// Log any objects that weren't found
	for _, name := range objectNames {
		if _, exists := result[name]; !exists {
			log.Warnw("No query object relationship map found", "object", name)
		}
	}

	return result, nil
}
