package cache

import (
	"context"
	"sync"
	"time"

	"github.com/bsm/redislock"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var once sync.Once
var redisClient *RedisClientImp
var redisClusterClient *RedisClusterClientImp

type IRedisClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value interface{}, ttl time.Duration) (string, error)
	Del(ctx context.Context, keys ...string) (int64, error)
	ObtainLock(ctx context.Context, key string, lockTTL time.Duration) (*redislock.Lock, error)
}

type RedisClusterClientImp struct {
	RedisClusterClient *redis.ClusterClient
	Logger             *zap.SugaredLogger
	ServiceName        string
}

type RedisClientImp struct {
	RedisClient *redis.Client
	Logger      *zap.SugaredLogger
	ServiceName string
}

// NewClusterRedisClient : Returns new redis client after initializing and validating the connection to the redis distributed cache
func NewClusterRedisClient(redisURL string, logger *zap.SugaredLogger, serviceName string) *RedisClusterClientImp {
	once.Do(func() {
		client := redis.NewClusterClient(&redis.ClusterOptions{Addrs: []string{redisURL}})
		_, err := client.Ping(context.Background()).Result()
		if err != nil {
			// os.Exit(1)
			logger.Error(err.Error())
		}
		redisClusterClient = &RedisClusterClientImp{
			RedisClusterClient: client,
			Logger:             logger,
			ServiceName:        serviceName,
		}
	})
	return redisClusterClient
}

// NewClusterRedisClientWithOptions : Returns new redis client after initializing and validating the connection to the redis distributed cache
func NewClusterRedisClientWithOptions(options *redis.ClusterOptions, logger *zap.SugaredLogger, serviceName string) *RedisClusterClientImp {
	once.Do(func() {
		client := redis.NewClusterClient(options)
		_, err := client.Ping(context.Background()).Result()
		if err != nil {
			// os.Exit(1)
			logger.Error(err.Error())
		}
		redisClusterClient = &RedisClusterClientImp{
			RedisClusterClient: client,
			Logger:             logger,
			ServiceName:        serviceName,
		}
	})
	return redisClusterClient
}

// NewRedisRegularClient : Returns new redis client after initializing and validating the connection to the redis distributed cache
func NewRedisRegularClient(logger *zap.SugaredLogger, redisURL, serviceName string) *RedisClientImp {
	once.Do(func() {
		client := redis.NewClient(&redis.Options{Addr: redisURL})
		_, err := client.Ping(context.Background()).Result()
		if err != nil {
			// os.Exit(1)
			logger.Error(err.Error())
		}
		redisClient = &RedisClientImp{
			RedisClient: client,
			Logger:      logger,
			ServiceName: serviceName,
		}
	})
	return redisClient
}

func (u *RedisClientImp) Get(ctx context.Context, key string) (string, error) {
	val, err := u.RedisClient.Get(ctx, u.ServiceName+key).Result()
	if err != nil {
		u.Logger.Error(err.Error())
	}
	return val, err
}

func (u *RedisClientImp) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) (string, error) {
	val, err := u.RedisClient.Set(ctx, u.ServiceName+key, value, ttl).Result()
	if err != nil {
		u.Logger.Error(err.Error())
	}
	return val, err
}

func (u *RedisClientImp) Del(ctx context.Context, keys ...string) (int64, error) {
	for i, value := range keys {
		keys[i] = u.ServiceName + value
	}
	val, err := u.RedisClient.Del(ctx, keys...).Result()
	if err != nil {
		u.Logger.Error(err.Error())
	}
	return val, err
}

func (u *RedisClientImp) ObtainLock(ctx context.Context, key string, lockTTL time.Duration) (*redislock.Lock, error) {
	locker := redislock.New(u.RedisClient)
	lock, err := locker.Obtain(ctx, key, lockTTL, nil)
	if err != nil {
		return nil, err
	}
	return lock, nil
}

func (u *RedisClusterClientImp) Get(ctx context.Context, key string) (string, error) {
	val, err := u.RedisClusterClient.Get(ctx, u.ServiceName+key).Result()
	if err != nil {
		u.Logger.Error(err.Error())
	}
	return val, err
}

func (u *RedisClusterClientImp) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) (string, error) {
	val, err := u.RedisClusterClient.Set(ctx, u.ServiceName+key, value, ttl).Result()
	if err != nil {
		u.Logger.Error(err.Error())
	}
	return val, err
}

func (u *RedisClusterClientImp) Del(ctx context.Context, keys ...string) (int64, error) {
	for i, value := range keys {
		keys[i] = u.ServiceName + value
	}
	val, err := u.RedisClusterClient.Del(ctx, keys...).Result()
	if err != nil {
		u.Logger.Error(err.Error())
	}
	return val, err
}
