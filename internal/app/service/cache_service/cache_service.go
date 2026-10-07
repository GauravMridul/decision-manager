package cache_service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	commoninit "decision-manager/internal/app/init"

	"github.com/dmi-infotech/common-modules/go/contracts"
	rediscache "github.com/dmi-infotech/common-modules/go/infrastructure/cache"
	"github.com/redis/go-redis/v9"
)

const (
	defaultScanCount     = int64(500)
	defaultDeleteBatch   = 200
)

type ICacheService interface {
	PingCache(ctx context.Context) error
	GetCacheStats(ctx context.Context) (map[string]interface{}, error)
	GetValueByKey(ctx context.Context, key string) (string, error)
	ClearCache(ctx context.Context, request ClearCacheRequest) (*ClearCacheResult, error)
	Close(ctx context.Context) error
}

type ClearCacheRequest struct {
	ClearAll      bool
	Match         string
	DryRun        bool
	LimitMatchKey int
}

type ClearCacheResult struct {
	Mode         string   `json:"mode"`
	Pattern      string   `json:"pattern"`
	DryRun       bool     `json:"dry_run"`
	KeysMatched  int      `json:"keys_matched"`
	KeysDeleted  int64    `json:"keys_deleted"`
	ScanCycles   int      `json:"scan_cycles"`
	Keys         []string `json:"keys"`
}

type CacheService struct {
	mu             sync.Mutex
	logger         contracts.Logger
	redisClient    redis.UniversalClient
	scanCount      int64
	deleteBatchLen int
}

func NewCacheService() *CacheService {
	logger := commoninit.GetLogger()

	service := &CacheService{
		logger:         logger,
		scanCount:      defaultScanCount,
		deleteBatchLen: defaultDeleteBatch,
	}

	if rc, ok := commoninit.GetCache().(*rediscache.RedisCache); ok {
		service.redisClient = rc.GetUniversalClient()
		logger.Info("Cache management using shared Redis client from common-modules")
	} else {
		logger.Warn("Cache provider is not *cache.RedisCache; cache management endpoints will be unavailable")
	}

	return service
}

func (s *CacheService) ClearCache(ctx context.Context, request ClearCacheRequest) (*ClearCacheResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if s.redisClient == nil {
		return nil, fmt.Errorf("cache clear redis client is not initialized")
	}

	pattern := "*"
	mode := "all"
	if !request.ClearAll {
		mode = "match"
		pattern = "*" + strings.TrimSpace(request.Match) + "*"
	}

	result := &ClearCacheResult{
		Mode:    mode,
		Pattern: pattern,
		DryRun:  request.DryRun,
		Keys:    make([]string, 0),
	}

	var (
		cursor uint64
		buffer = make([]string, 0, s.deleteBatchLen)
	)

	for {
		keys, nextCursor, err := s.redisClient.Scan(ctx, cursor, pattern, s.scanCount).Result()
		if err != nil {
			return nil, fmt.Errorf("redis scan failed for pattern %q: %w", pattern, err)
		}

		result.ScanCycles++
		result.KeysMatched += len(keys)
		result.Keys = appendLimitedKeys(result.Keys, keys, request.LimitMatchKey)

		for _, key := range keys {
			if request.DryRun {
				continue
			}

			buffer = append(buffer, key)
			if len(buffer) >= s.deleteBatchLen {
				deleted, delErr := s.deleteKeysClusterSafe(ctx, buffer)
				if delErr != nil {
					return nil, fmt.Errorf("redis delete failed: %w", delErr)
				}
				result.KeysDeleted += deleted
				buffer = buffer[:0]
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if !request.DryRun && len(buffer) > 0 {
		deleted, err := s.deleteKeysClusterSafe(ctx, buffer)
		if err != nil {
			return nil, fmt.Errorf("redis delete failed: %w", err)
		}
		result.KeysDeleted += deleted
	}

	s.logger.Infow("Cache clear execution completed",
		"mode", result.Mode,
		"pattern", result.Pattern,
		"dryRun", result.DryRun,
		"keysMatched", result.KeysMatched,
		"keysDeleted", result.KeysDeleted,
		"scanCycles", result.ScanCycles)

	return result, nil
}

func (s *CacheService) PingCache(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if s.redisClient == nil {
		return fmt.Errorf("cache redis client is not initialized")
	}
	return s.redisClient.Ping(ctx).Err()
}

func (s *CacheService) GetCacheStats(ctx context.Context) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if s.redisClient == nil {
		return nil, fmt.Errorf("cache redis client is not initialized")
	}

	stats := map[string]interface{}{
		"scan_count":       s.scanCount,
		"delete_batch_len": s.deleteBatchLen,
	}

	if info, err := s.redisClient.Info(ctx).Result(); err == nil {
		stats["redis_info"] = info
	} else {
		stats["redis_info_error"] = err.Error()
	}

	if dbSize, err := s.redisClient.DBSize(ctx).Result(); err == nil {
		stats["db_size"] = dbSize
	} else {
		stats["db_size_error"] = err.Error()
	}

	return stats, nil
}

func (s *CacheService) GetValueByKey(ctx context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if s.redisClient == nil {
		return "", fmt.Errorf("cache redis client is not initialized")
	}

	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return "", fmt.Errorf("cache key is required")
	}

	value, err := s.redisClient.Get(ctx, trimmedKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// Close is a no-op: the underlying Redis client is owned by common-modules and
// closed by commoninit.Shutdown(). It is kept to satisfy ICacheService.
func (s *CacheService) Close(_ context.Context) error {
	return nil
}

func (s *CacheService) deleteKeysClusterSafe(ctx context.Context, keys []string) (int64, error) {
	var totalDeleted int64
	for _, key := range keys {
		deleted, err := s.redisClient.Del(ctx, key).Result()
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += deleted
	}
	return totalDeleted, nil
}

func appendLimitedKeys(existing []string, incoming []string, limit int) []string {
	// limit <= 0 means "no cap" to preserve existing behavior.
	if limit <= 0 {
		return append(existing, incoming...)
	}
	// limit > 0
	remaining := limit - len(existing)
	if remaining <= 0 {
		return existing
	}
	if len(incoming) <= remaining {
		return append(existing, incoming...)
	}
	return append(existing, incoming[:remaining]...)
}

var _ ICacheService = (*CacheService)(nil)
