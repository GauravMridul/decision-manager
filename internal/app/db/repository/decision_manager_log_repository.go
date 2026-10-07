package repository

import (
	"context"
	"decision-manager/internal/app/db"
	"decision-manager/internal/app/models/decision_manager_log_models"
	"decision-manager/internal/app/types"
	"fmt"
	"time"

	commoninit "decision-manager/internal/app/init"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MaxMongoDocSizeBytes is the soft upper bound enforced before inserting a
// decision-manager log doc. MongoDB's hard limit is 16 MiB; we sit 1 MiB
// below to leave headroom for BSON framing and any field additions made
// between the size check and the actual driver-side marshal. Hitting this
// limit triggers a best-effort strip of the heavy preprocessing.values
// payload so the rest of the audit record still persists.
const MaxMongoDocSizeBytes = 15 * 1024 * 1024

// prepareDecisionManagerLogForInsert marshals the audit doc once, enforces
// the soft size limit, and (when over budget) strips the parsed "_pp*"
// trees from the preprocessing summary before re-marshaling. The returned
// bson.Raw can be handed directly to InsertOne / InsertMany so the driver
// does NOT pay a second marshal cost for the in-budget common path.
//
// Cost profile:
//   - In-budget doc  -> exactly one bson.Marshal pass (same work the driver
//     would have done internally anyway -- net zero overhead).
//   - Over-budget    -> two marshals + the strip walk (~ns). Strip-walk
//     touches a single map[string]interface{} entry, so the additional
//     wall-clock cost is dominated by the re-marshal, not the check itself.
//
// The function always runs in the async mongo worker (off the request
// critical path), so even the over-budget path adds at most a few ms to
// the worker insert latency -- never to the request response.
func prepareDecisionManagerLogForInsert(ctx context.Context, dml *decision_manager_log_models.DecisionManagerLog) (bson.Raw, error) {
	if dml == nil {
		return nil, fmt.Errorf("decisionManagerLog is nil")
	}
	log := commoninit.GetLogger(ctx)

	data, err := bson.Marshal(dml)
	if err != nil {
		return nil, fmt.Errorf("marshal decisionManagerLog: %w", err)
	}
	if len(data) <= MaxMongoDocSizeBytes {
		return bson.Raw(data), nil
	}

	originalSize := len(data)
	// Best-effort shrink: drop preprocessing.values (typically the heaviest
	// addition). Counters + per-rule outcomes stay so the audit trail is
	// still useful, and the same parsed trees can be re-derived from the
	// raw EsaResponseBody that remains in the doc.
	if stripPreprocessingValues(dml) {
		data, err = bson.Marshal(dml)
		if err != nil {
			return nil, fmt.Errorf("re-marshal after stripping preprocessing.values: %w", err)
		}
		log.Warnw("decision-manager log exceeded soft size limit; stripped preprocessing.values before insert",
			"originalSizeBytes", originalSize,
			"newSizeBytes", len(data),
			"limitBytes", MaxMongoDocSizeBytes,
			"customerId", customerIDOf(dml))
	}
	if len(data) > MaxMongoDocSizeBytes {
		// Either there was no preprocessing.values to drop, or stripping
		// it wasn't enough. Surface a loud error and still attempt the
		// insert so the failure mode is mongo's well-defined oversize
		// rejection (rather than us silently dropping the audit doc).
		log.Errorw("decision-manager log still exceeds size limit after stripping; insert will likely fail",
			"sizeBytes", len(data),
			"limitBytes", MaxMongoDocSizeBytes,
			"customerId", customerIDOf(dml))
	}
	return bson.Raw(data), nil
}

// customerIDOf safely extracts the customer id from a decision-manager log
// for correlation in repository-level error logs. Returns "" when the
// expected sub-struct is missing.
func customerIDOf(dml *decision_manager_log_models.DecisionManagerLog) string {
	if dml == nil || dml.EsaRequestBody == nil {
		return ""
	}
	return dml.EsaRequestBody.CustomerID
}

// stripPreprocessingValues removes the parsed "_pp*" value bodies from the
// preprocessing summary stored under ExtraFields["preprocessing"] when
// present. Sets ValuesStripped=true so downstream readers can tell the
// absence apart from "no preprocessing was configured". Returns true iff
// anything was actually removed.
func stripPreprocessingValues(dml *decision_manager_log_models.DecisionManagerLog) bool {
	if dml == nil || dml.ExtraFields == nil {
		return false
	}
	raw, ok := dml.ExtraFields["preprocessing"]
	if !ok {
		return false
	}
	summary, ok := raw.(*types.PreprocessingSummary)
	if !ok || summary == nil || len(summary.Values) == 0 {
		return false
	}
	summary.Values = nil
	summary.ValuesStripped = true
	return true
}

// IEsaLogRepository defines the interface for ESA log repository operations
type IDecisionManagerLogRepository interface {
	InsertLog(ctx context.Context, decisionManagerLog *decision_manager_log_models.DecisionManagerLog) error
	InsertLogs(ctx context.Context, decisionManagerLogs []*decision_manager_log_models.DecisionManagerLog) error
	FindLogsByFilter(ctx context.Context, filter bson.M, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error)
	UpdateLog(ctx context.Context, customerID string, update bson.M) error
	SoftDeleteLog(ctx context.Context, customerID string) error
	FindLogsByCustomerID(ctx context.Context, customerID string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error)
	FindLogsByJourneyID(ctx context.Context, journeyID string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error)
	FindLogsByApplicationID(ctx context.Context, applicationID string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error)
	FindLogsByPartnerName(ctx context.Context, partnerName string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error)
	FindLogsByStatus(ctx context.Context, status string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error)
	FindLogsByDateRange(ctx context.Context, startDate time.Time, endDate time.Time, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error)
}

// EsaLogRepository implements the IEsaLogRepository interface
type DecisionManagerLogRepository struct {
	collection *mongo.Collection
}

// NewEsaLogRepository creates a new instance of EsaLogRepository
func NewDecisionManagerLogRepository() *DecisionManagerLogRepository {
	collectionName := commoninit.GetConfigString("mongo.collections.decisionManagerLog")
	collection := db.GetMongoDB().Collection(collectionName)

	return &DecisionManagerLogRepository{
		collection: collection,
	}
}

// InsertLog inserts a new decision-manager log record into the database.
// The doc is size-guarded BEFORE insert: if it would exceed MongoDB's BSON
// limit, the heavy preprocessing.values payload is stripped first so the
// rest of the audit record (counters, per-rule outcomes, ESA request /
// response, SF composite, ...) still lands in mongo. The size check itself
// reuses the marshal output via bson.Raw, so the in-budget common path
// costs exactly one bson.Marshal pass (same work the driver would do).
func (r *DecisionManagerLogRepository) InsertLog(ctx context.Context, decisionManagerLog *decision_manager_log_models.DecisionManagerLog) error {
	log := commoninit.GetLogger(ctx)

	if decisionManagerLog.CreatedAt == 0 {
		decisionManagerLog.CreatedAt = time.Now().Unix()
	}

	doc, err := prepareDecisionManagerLogForInsert(ctx, decisionManagerLog)
	if err != nil {
		log.Errorw("Failed to prepare decision-manager log for insert", "error", err)
		return err
	}

	if _, err := r.collection.InsertOne(ctx, doc); err != nil {
		log.Errorw("Failed to insert decision-manager log", "error", err)
		return err
	}

	return nil
}

// InsertLogs inserts multiple decision-manager log records into the database
// in a single bulk operation. Each doc is independently size-guarded BEFORE
// the batch hits InsertMany (same logic as InsertLog), so one oversized
// entry can't force the whole batch to fail.
func (r *DecisionManagerLogRepository) InsertLogs(ctx context.Context, decisionManagerLogs []*decision_manager_log_models.DecisionManagerLog) error {
	log := commoninit.GetLogger(ctx)

	if len(decisionManagerLogs) == 0 {
		return nil
	}

	now := time.Now().Unix()

	documents := make([]interface{}, 0, len(decisionManagerLogs))
	for _, decisionManagerLog := range decisionManagerLogs {
		if decisionManagerLog == nil {
			continue
		}
		if decisionManagerLog.CreatedAt == 0 {
			decisionManagerLog.CreatedAt = now
		}
		doc, err := prepareDecisionManagerLogForInsert(ctx, decisionManagerLog)
		if err != nil {
			log.Errorw("Failed to prepare decision-manager log for bulk insert; skipping entry",
				"error", err,
				"customerId", customerIDOf(decisionManagerLog))
			continue
		}
		documents = append(documents, doc)
	}

	if len(documents) == 0 {
		return nil
	}

	result, err := r.collection.InsertMany(ctx, documents)
	if err != nil {
		log.Errorw("Failed to bulk insert decision-manager logs", "error", err, "count", len(documents))
		return err
	}

	log.Info("Successfully inserted decision-manager logs in bulk count: ", len(result.InsertedIDs))
	return nil
}

// FindLogsByFilter retrieves ESA log records matching the provided filter
func (r *DecisionManagerLogRepository) FindLogsByFilter(ctx context.Context, filter bson.M, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error) {
	log := commoninit.GetLogger(ctx)

	// Default filter to exclude deleted logs
	if filter == nil {
		filter = bson.M{}
	}
	if _, exists := filter["isDeleted"]; !exists {
		filter["isDeleted"] = false
	}

	// Set up options for pagination
	findOptions := options.Find()
	if limit > 0 {
		findOptions.SetLimit(limit)
	}
	if skip > 0 {
		findOptions.SetSkip(skip)
	}

	// Sort by createdAt in descending order (newest first)
	findOptions.SetSort(bson.M{"createdAt": -1})

	cursor, err := r.collection.Find(ctx, filter, findOptions)
	if err != nil {
		log.Errorw("Error finding ESA logs with filter", "filter", filter, "error", err)
		return nil, err
	}
	defer cursor.Close(ctx)

	var logs []*decision_manager_log_models.DecisionManagerLog
	for cursor.Next(ctx) {
		var esaLog decision_manager_log_models.DecisionManagerLog
		if err := cursor.Decode(&esaLog); err != nil {
			log.Errorw("Error decoding ESA log", "error", err)
			return nil, err
		}
		logs = append(logs, &esaLog)
	}

	if err := cursor.Err(); err != nil {
		log.Errorw("Cursor error while finding ESA logs", "error", err)
		return nil, err
	}

	return logs, nil
}

// UpdateLog updates an existing ESA log record
func (r *DecisionManagerLogRepository) UpdateLog(ctx context.Context, customerID string, update bson.M) error {
	log := commoninit.GetLogger(ctx)

	filter := bson.M{"customerId": customerID, "isDeleted": false}

	updateDoc := bson.M{"$set": update}

	result, err := r.collection.UpdateOne(ctx, filter, updateDoc)
	if err != nil {
		log.Errorw("Error updating ESA log", "customerId", customerID, "error", err)
		return err
	}

	if result.MatchedCount == 0 {
		log.Warnw("No ESA log found for customer", "customerId", customerID)
		return fmt.Errorf("no log found for customer %s", customerID)
	}

	return nil
}

// SoftDeleteLog marks an ESA log record as deleted by setting isDeleted to true
func (r *DecisionManagerLogRepository) SoftDeleteLog(ctx context.Context, customerID string) error {
	log := commoninit.GetLogger(ctx)

	filter := bson.M{"customerId": customerID, "isDeleted": false}

	update := bson.M{"$set": bson.M{"isDeleted": true, "updatedAt": time.Now().Unix()}}

	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		log.Errorw("Error soft-deleting ESA log", "customerId", customerID, "error", err)
		return err
	}

	if result.MatchedCount == 0 {
		log.Warnw("No ESA log found for customer for deletion", "customerId", customerID)
		return fmt.Errorf("no log found for customer %s", customerID)
	}

	return nil
}

// FindLogsByCustomerID retrieves ESA logs for a specific customer
func (r *DecisionManagerLogRepository) FindLogsByCustomerID(ctx context.Context, customerID string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error) {
	filter := bson.M{"customerId": customerID, "isDeleted": false}
	return r.FindLogsByFilter(ctx, filter, limit, skip)
}

// FindLogsByJourneyID retrieves ESA logs for a specific journey
func (r *DecisionManagerLogRepository) FindLogsByJourneyID(ctx context.Context, journeyID string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error) {
	filter := bson.M{"journeyId": journeyID, "isDeleted": false}
	return r.FindLogsByFilter(ctx, filter, limit, skip)
}

// FindLogsByApplicationID retrieves ESA logs for a specific application
func (r *DecisionManagerLogRepository) FindLogsByApplicationID(ctx context.Context, applicationID string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error) {
	filter := bson.M{"applicationId": applicationID, "isDeleted": false}
	return r.FindLogsByFilter(ctx, filter, limit, skip)
}

// FindLogsByPartnerName retrieves ESA logs for a specific partner
func (r *DecisionManagerLogRepository) FindLogsByPartnerName(ctx context.Context, partnerName string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error) {
	filter := bson.M{"partnerName": partnerName, "isDeleted": false}
	return r.FindLogsByFilter(ctx, filter, limit, skip)
}

// FindLogsByStatus retrieves ESA logs with a specific status
func (r *DecisionManagerLogRepository) FindLogsByStatus(ctx context.Context, status string, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error) {
	filter := bson.M{"status": status, "isDeleted": false}
	return r.FindLogsByFilter(ctx, filter, limit, skip)
}

// FindLogsByDateRange retrieves ESA logs within a date range
func (r *DecisionManagerLogRepository) FindLogsByDateRange(ctx context.Context, startDate time.Time, endDate time.Time, limit int64, skip int64) ([]*decision_manager_log_models.DecisionManagerLog, error) {
	filter := bson.M{
		"createdAt": bson.M{
			"$gte": startDate.Unix(),
			"$lte": endDate.Unix(),
		},
		"isDeleted": false,
	}
	return r.FindLogsByFilter(ctx, filter, limit, skip)
}
