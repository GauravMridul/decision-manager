package db

import (
	"context"
	"fmt"
	"time"

	commoninit "decision-manager/internal/app/init"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var mongoClient *mongo.Client
var mongoDB *mongo.Database

// To initialize the MongoDB connection
func InitMongoDB(ctx context.Context) {
	log := commoninit.GetLogger(ctx)

	// Access nested mongo config
	minPoolSize := commoninit.GetConfigInt("mongo.minPoolSize")
	maxPoolSize := commoninit.GetConfigInt("mongo.maxPoolSize")
	maxConnIdleTimeInMs := commoninit.GetConfigInt("mongo.maxConnIdleTimeInMs")
	connectionString := commoninit.GetConfigString("mongo.connectionString")
	dbName := commoninit.GetConfigString("mongo.dbName")

	if connectionString == "" {
		dbUser := commoninit.GetConfigString("mongo.username")
		dbPassword := commoninit.GetConfigString("mongo.password")
		dbHost := commoninit.GetConfigString("mongo.host")

		// Create connection string based on whether auth is needed
		if dbUser != "" && dbPassword != "" {
			connectionString = fmt.Sprintf("mongodb://%s:%s@%s", dbUser, dbPassword, dbHost)
		} else {
			connectionString = fmt.Sprintf("mongodb://%s", dbHost)
		}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	connectionOptions := options.Client().ApplyURI(connectionString)
	connectionOptions.SetMinPoolSize(uint64(minPoolSize))
	connectionOptions.SetMaxPoolSize(uint64(maxPoolSize))
	connectionOptions.SetMaxConnIdleTime(time.Duration(maxConnIdleTimeInMs) * time.Millisecond)

	client, err := mongo.Connect(timeoutCtx, connectionOptions)
	if err != nil {
		log.Errorw("Unable to connect with MongoDB", "error", err)
		return
	}

	err = client.Ping(timeoutCtx, nil)
	if err != nil {
		log.Errorw("Failed to ping MongoDB server", "error", err)
		return
	}

	log.Info("Successfully connected to MongoDB!")
	mongoClient = client
	mongoDB = client.Database(dbName)
}

// GetMongoClient returns the MongoDB client
func GetMongoClient() *mongo.Client {
	return mongoClient
}

// GetMongoDB returns the MongoDB database
func GetMongoDB() *mongo.Database {
	return mongoDB
}

// DisconnectMongoDB gracefully disconnects the MongoDB client, flushing any
// pending operations and releasing connection pool resources.
// Nilifies globals before Disconnect so GetMongoClient/GetMongoDB never return
// a client that is being torn down. Uses a 10s cap on disconnect (ESA-aligned).
func DisconnectMongoDB(ctx context.Context) error {
	client := mongoClient
	mongoClient = nil
	mongoDB = nil
	if client == nil {
		return nil
	}
	// Use a fresh timeout: parent shutdown ctx may already be expired after prior hooks
	// (e.g. long async log Close); disconnect still needs time to flush the pool.
	disconnectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Disconnect(disconnectCtx); err != nil {
		// Avoid logger lookups during shutdown hooks; they may contend with global locks.
		fmt.Printf("MongoDB disconnect failed during shutdown: %v\n", err)
		return fmt.Errorf("mongo disconnect: %w", err)
	}
	fmt.Println("MongoDB client disconnected successfully")
	return nil
}
