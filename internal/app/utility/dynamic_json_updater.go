package utility

import (
	"bytes"
	"context"
	"crypto/sha256"
	"decision-manager/internal/app/constants"
	commoninit "decision-manager/internal/app/init"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"decision-manager/internal/app/types"

	"github.com/dmi-infotech/common-modules/go/contracts"

	"github.com/Knetic/govaluate"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsv2config "github.com/aws/aws-sdk-go-v2/config"
)

// getRequestLogger returns the request-scoped logger from ctx if set; otherwise returns fallback.
// Use this so all logging in request path includes correlation_id and other request context.
func getRequestLogger(ctx context.Context, fallback contracts.Logger) contracts.Logger {
	if ctx == nil {
		return fallback
	}
	if log := GetRequestLogger(ctx); log != nil {
		return log
	}
	return fallback
}

// Import types from separate package to avoid circular imports
type SFRequest = types.SFRequest
type SFRequestDB = types.SFRequestDB
type LookupConfig = types.LookupConfig

// Custom error type for missing parameters
type MissingParameterError struct {
	Err error
}

func (e *MissingParameterError) Error() string {
	return e.Err.Error()
}

// DynamicJsonUpdater handles dynamic JSON updates with template interpolation
// type DynamicJsonUpdater struct{}

// DynamicJsonUpdater handles dynamic JSON updates with template interpolation
type DynamicJsonUpdater struct {
	// arrayConfig is accessed atomically so that hot-reload via UpdateArrayConfig
	// does not race with concurrent InterpolateValues goroutines reading it.
	arrayConfig atomic.Pointer[ArrayProcessingConfig]
	log         contracts.Logger
	// MaxParallelGoroutines caps concurrent goroutines per request (0 = no cap)
	MaxParallelGoroutines int
}

func (u *DynamicJsonUpdater) getArrayConfig() *ArrayProcessingConfig {
	return u.arrayConfig.Load()
}

func (u *DynamicJsonUpdater) setArrayConfig(cfg *ArrayProcessingConfig) {
	u.arrayConfig.Store(cfg)
}

// ArrayProcessingConfig holds configuration for array processing optimizations
// Memory pool settings are always loaded from config.json with fallback defaults
type ArrayProcessingConfig struct {
	MemoryPoolThreshold      int  `json:"memoryPoolThreshold"`      // Threshold for enabling memory pooling (from config.json, default: 20)
	EnableMemoryPool         bool `json:"enableMemoryPool"`         // Whether to use memory pooling (from config.json, default: true)
	FallbackToSingleElement  bool `json:"fallbackToSingleElement"`  // Fallback to single element if array data is not found (from config.json, default: false)
	SkipInvalidArrayPaths    bool `json:"skipInvalidArrayPaths"`    // Skip invalid paths gracefully
	CollectUnresolvedDetails bool `json:"collectUnresolvedDetails"` // Collect unresolved field details (expensive for large arrays)
}

// UnresolvedFields holds information about fields that couldn't be resolved during processing
type UnresolvedFields struct {
	Expressions           []string // Fields that failed due to missing parameters in expressions
	FailedExpressions     []string // Fields that failed due to expression evaluation errors
	DotPaths              []string // Fields that failed due to unresolved dot paths
	SFVariables           []string // Fields that failed due to unresolved SF variables
	ExpressionCount       int
	FailedExpressionCount int
	DotPathCount          int
	SFVariableCount       int
}

// NewDynamicJsonUpdater creates a new DynamicJsonUpdater instance
func NewDynamicJsonUpdater() *DynamicJsonUpdater {
	u := &DynamicJsonUpdater{
		log:                   commoninit.GetLogger(),
		MaxParallelGoroutines: commoninit.GetConfigInt(constants.ServerMaxParallelDynamicJsonGoroutines, 0),
	}
	u.setArrayConfig(loadArrayConfigFromFile())
	return u
}

// loadArrayConfigFromFile loads array processing configuration from config.json with defaults
func loadArrayConfigFromFile() *ArrayProcessingConfig {
	config := &ArrayProcessingConfig{
		MemoryPoolThreshold:      commoninit.GetConfigInt("arrayProcessing.memoryPoolThreshold", 20),          // Default: 20
		EnableMemoryPool:         commoninit.GetConfigBool("arrayProcessing.enableMemoryPool", true),          // Default: true
		FallbackToSingleElement:  commoninit.GetConfigBool("arrayProcessing.fallbackToSingleElement", false),  // Default: false
		SkipInvalidArrayPaths:    commoninit.GetConfigBool("arrayProcessing.skipInvalidArrayPaths", false),    // Default: false
		CollectUnresolvedDetails: commoninit.GetConfigBool("arrayProcessing.collectUnresolvedDetails", false), // Default: false
	}

	// Log the loaded configuration
	commoninit.GetLogger().Info("Array processing config loaded - MemoryPoolThreshold: ", config.MemoryPoolThreshold, " EnableMemoryPool: ", config.EnableMemoryPool, " FallbackToSingleElement: ", config.FallbackToSingleElement, " SkipInvalidArrayPaths: ", config.SkipInvalidArrayPaths, " CollectUnresolvedDetails: ", config.CollectUnresolvedDetails)

	return config
}

// NewDynamicJsonUpdaterWithConfig creates a new DynamicJsonUpdater with custom config.
// Falls back to config.json defaults for any field not set by the caller.
func NewDynamicJsonUpdaterWithConfig(config *ArrayProcessingConfig) *DynamicJsonUpdater {
	arrayConfig := loadArrayConfigFromFile()

	if config != nil {
		if config.MemoryPoolThreshold != 0 {
			arrayConfig.MemoryPoolThreshold = config.MemoryPoolThreshold
		}
		arrayConfig.EnableMemoryPool = config.EnableMemoryPool
		arrayConfig.FallbackToSingleElement = config.FallbackToSingleElement
		arrayConfig.SkipInvalidArrayPaths = config.SkipInvalidArrayPaths
		arrayConfig.CollectUnresolvedDetails = config.CollectUnresolvedDetails
	}

	u := &DynamicJsonUpdater{
		log: commoninit.GetLogger(),
	}
	u.setArrayConfig(arrayConfig)
	return u
}

// Memory pools for high-volume scenarios
var (
	requestPool = sync.Pool{
		New: func() interface{} {
			return SFRequest{
				Body: make(map[string]interface{}, 10),
			}
		},
	}

	bodyPool = sync.Pool{
		New: func() interface{} {
			return make(map[string]interface{}, 10)
		},
	}
)

// Global caches.
// exprCachePtr is accessed via atomic.Pointer so that resetExprCache can
// swap the entire map without racing with concurrent Load/Store callers.
var (
	exprCachePtr atomic.Pointer[sync.Map]

	exprCacheEntryCount atomic.Int64
	cacheResetInFlight  atomic.Bool // CAS guard to prevent thundering-herd resets
	cacheLimitsOnce     sync.Once
	exprCacheMaxEntries int64 = 500
	cacheEntryTTL             = 15 * time.Minute

	globalWorkerSem     chan struct{}
	globalWorkerSemOnce sync.Once
)

func init() {
	exprCachePtr.Store(&sync.Map{})
}

func getExprCache() *sync.Map {
	return exprCachePtr.Load()
}

type exprCacheEntry struct {
	expr      *govaluate.EvaluableExpression
	expiresAt int64
}

// DynamicJSONCacheStats is a lightweight snapshot of current cache pressure.
type DynamicJSONCacheStats struct {
	ExprCacheEntries         int64
	ExprCacheMaxEntries      int64
	ParameterCacheEntries    int64
	ParameterCacheMaxEntries int64
}

// DynamicJSONCacheSample provides bounded, cheap sampling to estimate cache footprint trends.
type DynamicJSONCacheSample struct {
	ExprSamples            int
	AvgExprKeyBytes        float64
	ParameterSamples       int
	AvgParameterKeyBytes   float64
	AvgParameterMapEntries float64
	MaxParameterMapEntries int
}

func loadDynamicCacheLimits() {
	cacheLimitsOnce.Do(func() {
		if !commoninit.IsConfigAvailable() {
			return
		}
		exprCap := commoninit.GetConfigInt(constants.ServerDynamicJsonExprCacheMaxEntries, 0)
		if exprCap <= 0 {
			// Backward compatibility for earlier key naming.
			exprCap = commoninit.GetConfigInt("dynamic_json.expr_cache_max_entries", int(exprCacheMaxEntries))
		}
		if exprCap > 0 {
			exprCacheMaxEntries = int64(exprCap)
		}
		ttlSeconds := commoninit.GetConfigInt("server.dynamic_json_cache_ttl_seconds", 0)
		if ttlSeconds > 0 {
			cacheEntryTTL = time.Duration(ttlSeconds) * time.Second
		}

		if commoninit.IsLoggerAvailable() {
			commoninit.GetLogger().Infow("Dynamic JSON cache limits loaded",
				"exprCacheMaxEntries", exprCacheMaxEntries,
				"cacheEntryTTLSeconds", int(cacheEntryTTL.Seconds()))
		}

		startCacheSweeper()
	})
}

func getGlobalWorkerSemaphore() chan struct{} {
	globalWorkerSemOnce.Do(func() {
		maxWorkers := commoninit.GetConfigInt(constants.ServerMaxGlobalWorkerGoroutines, 0)
		if maxWorkers > 0 {
			globalWorkerSem = make(chan struct{}, maxWorkers)
			if commoninit.IsLoggerAvailable() {
				commoninit.GetLogger().Infow("Dynamic JSON global worker semaphore enabled", "maxWorkers", maxWorkers)
			}
		}
	})
	return globalWorkerSem
}

// resetExprCache atomically replaces the expression cache with an empty map.
// Uses a CAS guard so that if multiple goroutines race to reset at the same
// time (thundering herd), only the first one performs the swap.
func resetExprCache(reason string) {
	if !cacheResetInFlight.CompareAndSwap(false, true) {
		return // another goroutine is already resetting
	}
	exprCachePtr.Store(&sync.Map{})
	exprCacheEntryCount.Store(0)
	cacheResetInFlight.Store(false)
	if commoninit.IsLoggerAvailable() {
		commoninit.GetLogger().Warnw("Expression cache reset for memory control", "reason", reason, "maxEntries", exprCacheMaxEntries)
	}
}

var cacheSweepOnce sync.Once

// startCacheSweeper runs a background goroutine that periodically removes
// expired entries from the expression cache so they don't accumulate when
// traffic patterns change and certain keys are never accessed again.
func startCacheSweeper() {
	cacheSweepOnce.Do(func() {
		commoninit.StartTrackedGoroutine(func() {
			interval := cacheEntryTTL
			if interval < time.Minute {
				interval = time.Minute
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			shutdownCh := commoninit.GetAppShutdownSignal()
			for {
				select {
				case <-ticker.C:
					now := time.Now().UnixNano()
					cache := getExprCache()
					var swept int64
					cache.Range(func(key, value interface{}) bool {
						if entry, ok := value.(exprCacheEntry); ok {
							if cacheEntryTTL > 0 && now > entry.expiresAt {
								if _, loaded := cache.LoadAndDelete(key); loaded {
									if cache == getExprCache() {
										exprCacheEntryCount.Add(-1)
										swept++
									}
								}
							}
						}
						return true
					})
					if swept > 0 && commoninit.IsLoggerAvailable() {
						commoninit.GetLogger().Infow("Expression cache sweep completed", "entriesRemoved", swept, "remaining", exprCacheEntryCount.Load())
					}
				case <-shutdownCh:
					return
				}
			}
		})
	})
}

// GetDynamicJSONCacheStats returns cache counters for telemetry.
func GetDynamicJSONCacheStats() DynamicJSONCacheStats {
	return DynamicJSONCacheStats{
		ExprCacheEntries:         exprCacheEntryCount.Load(),
		ExprCacheMaxEntries:      exprCacheMaxEntries,
		ParameterCacheEntries:    0,
		ParameterCacheMaxEntries: 0,
	}
}

// SampleDynamicJSONCacheStats returns bounded sample estimates for cache key sizes and map fan-out.
// maxEntries is clamped to a minimum of 1 to keep sampling bounded and safe.
func SampleDynamicJSONCacheStats(maxEntries int) DynamicJSONCacheSample {
	if maxEntries < 1 {
		maxEntries = 1
	}

	sample := DynamicJSONCacheSample{}

	var exprKeyBytes int
	getExprCache().Range(func(key, _ interface{}) bool {
		keyStr, ok := key.(string)
		if !ok {
			return true
		}
		sample.ExprSamples++
		exprKeyBytes += len(keyStr)
		return sample.ExprSamples < maxEntries
	})
	if sample.ExprSamples > 0 {
		sample.AvgExprKeyBytes = float64(exprKeyBytes) / float64(sample.ExprSamples)
	}

	return sample
}

func expressionArgToString(arg interface{}) string {
	if arg == nil {
		return ""
	}
	return fmt.Sprintf("%v", arg)
}

func expressionArgToInt(arg interface{}) (int, bool) {
	switch v := arg.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float32:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func expressionArgToFloat64(arg interface{}) (float64, bool) {
	switch v := arg.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// stripToDigits removes every character that is not an ASCII digit (0-9).
// Used by stringToInt to tolerate formatted values like "20,000/weekly" → "20000".
func stripToDigits(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// stripToDigitsAndDot removes every character except ASCII digits and the first
// decimal point. Used by stringToDouble to tolerate values like "1,234.56/month" → "1234.56".
func stripToDigitsAndDot(s string) string {
	var b strings.Builder
	dotSeen := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			b.WriteByte(c)
		} else if c == '.' && !dotSeen {
			b.WriteByte(c)
			dotSeen = true
		}
	}
	return b.String()
}

// truncateStringByMaxChars truncates by rune count without allocating []rune.
func truncateStringByMaxChars(value string, maxChars int) string {
	if maxChars < 0 {
		return value
	}
	if maxChars == 0 {
		return ""
	}
	runePos := 0
	for byteIdx := range value {
		if runePos == maxChars {
			return value[:byteIdx]
		}
		runePos++
	}
	return value
}

// sliceStringByRuneRange slices string [start,end) in rune-space without []rune allocation.
func sliceStringByRuneRange(value string, start int, end int) string {
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if start >= end {
		return ""
	}

	// Fast path when the caller asks from the beginning.
	if start == 0 {
		return truncateStringByMaxChars(value, end)
	}

	runePos := 0
	startByte := -1
	for byteIdx := range value {
		if runePos == start {
			startByte = byteIdx
		}
		if runePos == end {
			if startByte == -1 {
				startByte = byteIdx
			}
			return value[startByte:byteIdx]
		}
		runePos++
	}

	if startByte == -1 {
		if runePos == start {
			startByte = len(value)
		} else {
			return ""
		}
	}
	return value[startByte:]
}

// truncateValuePreserveType trims string-like data while preserving input shape/type.
// It uses structural sharing and only allocates containers when a nested value changes.
func truncateValuePreserveType(value interface{}, maxChars int) (interface{}, bool) {
	switch v := value.(type) {
	case string:
		truncated := truncateStringByMaxChars(v, maxChars)
		return truncated, truncated != v
	case []byte:
		truncated := truncateStringByMaxChars(string(v), maxChars)
		if len(truncated) == len(v) && truncated == string(v) {
			return v, false
		}
		return []byte(truncated), true
	case []interface{}:
		var out []interface{}
		for i, item := range v {
			truncatedItem, changed := truncateValuePreserveType(item, maxChars)
			if !changed {
				continue
			}
			if out == nil {
				out = make([]interface{}, len(v))
				copy(out, v)
			}
			out[i] = truncatedItem
		}
		if out == nil {
			return v, false
		}
		return out, true
	case map[string]interface{}:
		var out map[string]interface{}
		for key, item := range v {
			truncatedItem, changed := truncateValuePreserveType(item, maxChars)
			if !changed {
				continue
			}
			if out == nil {
				out = make(map[string]interface{}, len(v))
				for existingKey, existingVal := range v {
					out[existingKey] = existingVal
				}
			}
			out[key] = truncatedItem
		}
		if out == nil {
			return v, false
		}
		return out, true
	default:
		return value, false
	}
}

// truncateRawStringValue converts input to string with fast paths, then slices by rune range.
func truncateRawStringValue(value interface{}, start int, end int) string {
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	case map[string]interface{}:
		// Deterministic representation for maps (stable key ordering via JSON).
		jsonBytes, err := marshalFastSafe(v)
		if err != nil {
			raw = fmt.Sprintf("%v", value)
		} else {
			raw = string(jsonBytes)
		}
	default:
		raw = fmt.Sprintf("%v", value)
	}
	return sliceStringByRuneRange(raw, start, end)
}

func splitJSONAndHTMLParts(value interface{}) (string, string) {
	raw := expressionArgToString(value)
	if raw == "" {
		return "", ""
	}
	lower := strings.ToLower(raw)
	index := strings.Index(lower, "<html")
	if index == -1 {
		return strings.TrimSpace(raw), ""
	}
	return strings.TrimSpace(raw[:index]), raw[index:]
}

var knownDelimiterRegex = regexp.MustCompile(`[;,|]+`)

func splitByKnownDelimiters(raw string) []string {
	return knownDelimiterRegex.Split(raw, -1)
}

func delimetterSwapValue(raw string, outputDelimiter string, sourceDelimiter string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.TrimSpace(outputDelimiter) == "" {
		outputDelimiter = ";"
	}

	var tokens []string
	if strings.TrimSpace(sourceDelimiter) != "" {
		tokens = strings.Split(raw, sourceDelimiter)
	} else {
		// Default mode: normalize common delimiters to one output delimiter.
		tokens = splitByKnownDelimiters(raw)
	}

	cleaned := make([]string, 0, len(tokens))
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, trimmed)
	}
	return strings.Join(cleaned, outputDelimiter)
}

func buildS3ObjectKey(filePath string, extension string) string {
	normalizedPath := strings.TrimSpace(filePath)
	normalizedPath = strings.ReplaceAll(normalizedPath, "\\", "/")
	normalizedPath = strings.TrimPrefix(normalizedPath, "/")
	normalizedPath = strings.TrimSuffix(normalizedPath, "/")

	normalizedPath = strings.ReplaceAll(normalizedPath, " ", "_")
	normalizedPath = strings.ReplaceAll(normalizedPath, ":", "_")

	normalizedExtension := strings.TrimSpace(extension)
	if normalizedExtension != "" && !strings.HasPrefix(normalizedExtension, ".") {
		normalizedExtension = "." + normalizedExtension
	}

	if normalizedPath == "" {
		if normalizedExtension != "" {
			// Ensure extension-only uploads still produce a valid filename, not a bare token.
			return "file" + normalizedExtension
		}
		return ""
	}
	if normalizedExtension != "" && !strings.HasSuffix(strings.ToLower(normalizedPath), strings.ToLower(normalizedExtension)) {
		normalizedPath += normalizedExtension
	}
	return normalizedPath
}

func buildS3PublicURL(bucket string, region string, objectKey string) string {
	escapedSegments := make([]string, 0, len(strings.Split(objectKey, "/")))
	for _, segment := range strings.Split(objectKey, "/") {
		if segment == "" {
			continue
		}
		escapedSegments = append(escapedSegments, path.Clean("/" + segment)[1:])
	}
	safeKey := strings.Join(escapedSegments, "/")
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, safeKey)
}

func uploadToS3WithConfig(content string, extension string, filePath string, bucket string, region string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("empty upload content")
	}
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return "", fmt.Errorf("bucket name is required")
	}

	objectKey := buildS3ObjectKey(filePath, extension)
	if objectKey == "" {
		return "", fmt.Errorf("computed S3 object key is empty")
	}

	region = strings.TrimSpace(region)
	if region == "" {
		return "", fmt.Errorf("region is required")
	}
	awsCfg, err := awsv2config.LoadDefaultConfig(
		context.Background(),
		awsv2config.WithRegion(region),
	)
	if err != nil {
		return "", fmt.Errorf("failed to load aws config: %w", err)
	}
	creds, err := awsCfg.Credentials.Retrieve(context.Background())
	if err != nil {
		return "", fmt.Errorf("failed to resolve aws credentials: %w", err)
	}

	contentType := strings.TrimSpace(mime.TypeByExtension(strings.TrimSpace(extension)))
	if contentType == "" {
		if strings.EqualFold(strings.TrimSpace(extension), ".html") || strings.EqualFold(strings.TrimSpace(extension), "html") {
			contentType = "text/html; charset=utf-8"
		} else {
			contentType = "application/octet-stream"
		}
	}

	escapedSegments := make([]string, 0, len(strings.Split(objectKey, "/")))
	for _, segment := range strings.Split(objectKey, "/") {
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" {
			continue
		}
		escapedSegments = append(escapedSegments, url.PathEscape(trimmed))
	}
	escapedKey := strings.Join(escapedSegments, "/")
	endpoint := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, escapedKey)
	contentBytes := []byte(content)
	payloadHashBytes := sha256.Sum256(contentBytes)
	payloadHash := hex.EncodeToString(payloadHashBytes[:])

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, endpoint, bytes.NewReader(contentBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create S3 request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", fmt.Sprintf("%s.s3.%s.amazonaws.com", bucket, region))
	req.Header.Set("x-amz-content-sha256", payloadHash)
	signTime := time.Now().UTC()
	signer := v4.NewSigner()
	if err := signer.SignHTTP(context.Background(), creds, req, payloadHash, "s3", region, signTime); err != nil {
		return "", fmt.Errorf("failed to sign s3 request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to upload object to s3: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("s3 upload failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	return buildS3PublicURL(bucket, region, objectKey), nil
}

// Custom functions (simple, no complex checks)
var customFunctions = map[string]govaluate.ExpressionFunction{
	"min": func(args ...interface{}) (interface{}, error) {
		min := args[0].(float64)
		for _, arg := range args {
			if val := arg.(float64); val < min {
				min = val
			}
		}
		return min, nil
	},
	"max": func(args ...interface{}) (interface{}, error) {
		max := args[0].(float64)
		for _, arg := range args {
			if val := arg.(float64); val > max {
				max = val
			}
		}
		return max, nil
	},
	"concat": func(args ...interface{}) (interface{}, error) {
		var builder strings.Builder
		for _, arg := range args {
			builder.WriteString(fmt.Sprintf("%v", arg))
		}
		return builder.String(), nil
	},
	"intToString": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return "", nil
		}
		if n, ok := expressionArgToInt(args[0]); ok {
			return strconv.Itoa(n), nil
		}
		return expressionArgToString(args[0]), nil
	},
	"stringToInt": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return 0, nil
		}
		// For non-string numeric types, use the shared helper directly.
		if _, isStr := args[0].(string); !isStr {
			if n, ok := expressionArgToInt(args[0]); ok {
				return n, nil
			}
			return 0, nil
		}
		// For strings: keep only digits 0-9 and the first decimal point,
		// parse as float64, then truncate to int.
		// e.g. "20,000/weekly" → "20000"   → 20000
		//      "1,234.56/mo"  → "1234.56" → 1234
		cleaned := stripToDigitsAndDot(args[0].(string))
		if cleaned == "" || cleaned == "." {
			return 0, nil
		}
		f, err := strconv.ParseFloat(cleaned, 64)
		if err != nil {
			return 0, nil
		}
		return int(f), nil
	},
	"stringToDouble": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return float64(0), nil
		}
		// For non-string numeric types, use the shared helper directly.
		if _, isStr := args[0].(string); !isStr {
			if f, ok := expressionArgToFloat64(args[0]); ok {
				return f, nil
			}
			return float64(0), nil
		}
		// For strings: keep only digits 0-9 and the first decimal point,
		// discard everything else (commas, currency symbols, units, etc.).
		// e.g. "1,234.56/month" → "1234.56" → 1234.56
		cleaned := stripToDigitsAndDot(args[0].(string))
		if cleaned == "" || cleaned == "." {
			return float64(0), nil
		}
		f, err := strconv.ParseFloat(cleaned, 64)
		if err != nil {
			return float64(0), nil
		}
		return f, nil
	},
	"doubleToString": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return "", nil
		}
		if f, ok := expressionArgToFloat64(args[0]); ok {
			return strconv.FormatFloat(f, 'f', -1, 64), nil
		}
		return expressionArgToString(args[0]), nil
	},
	"toLower": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return "", nil
		}
		return strings.ToLower(fmt.Sprintf("%v", args[0])), nil
	},
	"toUpper": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return "", nil
		}
		return strings.ToUpper(fmt.Sprintf("%v", args[0])), nil
	},
	"left": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return "", nil
		}
		value := expressionArgToString(args[0])
		if len(args) < 2 {
			return value, nil
		}
		length, ok := expressionArgToInt(args[1])
		if !ok || length < 0 {
			return value, nil
		}
		if length >= len(value) {
			return value, nil
		}
		return value[:length], nil
	},
	"right": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return "", nil
		}
		value := expressionArgToString(args[0])
		if len(args) < 2 {
			return value, nil
		}
		length, ok := expressionArgToInt(args[1])
		if !ok || length < 0 {
			return value, nil
		}
		if length >= len(value) {
			return value, nil
		}
		return value[len(value)-length:], nil
	},
	// Keep substring behavior aligned with ESA string transformation semantics:
	// invalid numeric inputs fall back to original value, out-of-range start returns empty string.
	"substring": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 || args[0] == nil {
			return "", nil
		}
		value := expressionArgToString(args[0])
		if len(args) < 2 {
			return value, nil
		}
		startIndex, ok := expressionArgToInt(args[1])
		if !ok || startIndex < 0 {
			return value, nil
		}
		if startIndex >= len(value) {
			return "", nil
		}
		if len(args) == 2 {
			return value[startIndex:], nil
		}
		length, ok := expressionArgToInt(args[2])
		if !ok || length < 0 {
			return value, nil
		}
		endIndex := startIndex + length
		if endIndex > len(value) {
			endIndex = len(value)
		}
		return value[startIndex:endIndex], nil
	},
	"delimetterSwap": func(args ...interface{}) (interface{}, error) {
		// Usage:
		// delimetterSwap(value, outputDelimiter)
		// delimetterSwap(value, outputDelimiter, sourceDelimiter)
		if len(args) == 0 {
			return "", nil
		}
		value := expressionArgToString(args[0])
		outputDelimiter := ";"
		if len(args) > 1 {
			outputDelimiter = expressionArgToString(args[1])
		}
		sourceDelimiter := ""
		if len(args) > 2 {
			sourceDelimiter = expressionArgToString(args[2])
		}
		return delimetterSwapValue(value, outputDelimiter, sourceDelimiter), nil
	},
	"inList": func(args ...interface{}) (interface{}, error) {
		if len(args) < 2 {
			return false, fmt.Errorf("inList requires at least 2 arguments")
		}
		candidates := make(map[string]struct{}, len(args)-1)
		for i := 1; i < len(args); i++ {
			candidates[fmt.Sprintf("%v", args[i])] = struct{}{}
		}

		matches := func(val string) bool {
			_, ok := candidates[val]
			return ok
		}

		switch arr := args[0].(type) {
		case []interface{}:
			for _, item := range arr {
				if item == nil {
					continue
				}
				if matches(fmt.Sprintf("%v", item)) {
					return true, nil
				}
			}
			return false, nil
		case []string:
			for _, item := range arr {
				if matches(item) {
					return true, nil
				}
			}
			return false, nil
		case []int:
			for _, item := range arr {
				if matches(strconv.Itoa(item)) {
					return true, nil
				}
			}
			return false, nil
		case []int32:
			for _, item := range arr {
				if matches(strconv.FormatInt(int64(item), 10)) {
					return true, nil
				}
			}
			return false, nil
		case []int64:
			for _, item := range arr {
				if matches(strconv.FormatInt(item, 10)) {
					return true, nil
				}
			}
			return false, nil
		case []float32:
			for _, item := range arr {
				if matches(strconv.FormatFloat(float64(item), 'g', -1, 32)) {
					return true, nil
				}
			}
			return false, nil
		case []float64:
			for _, item := range arr {
				if matches(strconv.FormatFloat(item, 'g', -1, 64)) {
					return true, nil
				}
			}
			return false, nil
		default:
			if matches(fmt.Sprintf("%v", args[0])) {
				return true, nil
			}
			return false, nil
		}
	},
	"inListField": func(args ...interface{}) (interface{}, error) {
		if len(args) < 3 {
			return false, fmt.Errorf("inListField requires at least 3 arguments")
		}

		toComparableString := func(v interface{}) string {
			switch x := v.(type) {
			case string:
				return x
			case int:
				return strconv.Itoa(x)
			case int32:
				return strconv.FormatInt(int64(x), 10)
			case int64:
				return strconv.FormatInt(x, 10)
			case float32:
				return strconv.FormatFloat(float64(x), 'g', -1, 32)
			case float64:
				return strconv.FormatFloat(x, 'g', -1, 64)
			case bool:
				return strconv.FormatBool(x)
			default:
				return fmt.Sprintf("%v", x)
			}
		}

		buildCandidateSet := func(start int) map[string]struct{} {
			candidates := make(map[string]struct{}, len(args)-start)
			for i := start; i < len(args); i++ {
				candidates[toComparableString(args[i])] = struct{}{}
			}
			return candidates
		}

		// Normal path: args[0] is the array (not expanded by govaluate).
		if arr, ok := args[0].([]interface{}); ok {
			fieldName := toComparableString(args[1])
			if fieldName == "" {
				return false, fmt.Errorf("inListField requires a non-empty field name")
			}

			candidates := buildCandidateSet(2)
			if len(candidates) == 0 {
				return false, nil
			}

			for _, item := range arr {
				m, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				if v, exists := m[fieldName]; exists {
					if _, found := candidates[toComparableString(v)]; found {
						return true, nil
					}
				}
			}
			return false, nil
		}

		// Expanded array path: govaluate may flatten array items into individual map arguments.
		if _, isMap := args[0].(map[string]interface{}); isMap {
			fieldNameIdx := -1
			for i := 1; i < len(args); i++ {
				if _, ok := args[i].(string); ok {
					fieldNameIdx = i
					break
				}
			}
			if fieldNameIdx < 1 || fieldNameIdx >= len(args)-1 {
				return false, nil
			}

			fieldName := args[fieldNameIdx].(string)
			if fieldName == "" {
				return false, fmt.Errorf("inListField requires a non-empty field name")
			}

			candidates := buildCandidateSet(fieldNameIdx + 1)
			if len(candidates) == 0 {
				return false, nil
			}

			for i := 0; i < fieldNameIdx; i++ {
				m, ok := args[i].(map[string]interface{})
				if !ok {
					continue
				}
				if v, exists := m[fieldName]; exists {
					if _, found := candidates[toComparableString(v)]; found {
						return true, nil
					}
				}
			}
		}
		return false, nil
	},
	"pow": func(args ...interface{}) (interface{}, error) {
		return math.Pow(args[0].(float64), args[1].(float64)), nil
	},
	"sqrt": func(args ...interface{}) (interface{}, error) {
		return math.Sqrt(args[0].(float64)), nil
	},
	"abs": func(args ...interface{}) (interface{}, error) {
		return math.Abs(args[0].(float64)), nil
	},
	"round": func(args ...interface{}) (interface{}, error) {
		return math.Round(args[0].(float64)), nil
	},
	"ceil": func(args ...interface{}) (interface{}, error) {
		return math.Ceil(args[0].(float64)), nil
	},
	"floor": func(args ...interface{}) (interface{}, error) {
		return math.Floor(args[0].(float64)), nil
	},
	"serializeJson": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return "", fmt.Errorf("serializeJson function requires at least one argument")
		}

		jsonBytes, err := marshalFastSafe(args[0])
		if err != nil {
			return "", fmt.Errorf("failed to marshal to JSON: %v", err)
		}
		return string(jsonBytes), nil
	},
	"serializeJsonPretty": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return "", fmt.Errorf("serializeJsonPretty function requires at least one argument")
		}

		jsonBytes, err := marshalIndentFastSafe(args[0], "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal to pretty JSON: %v", err)
		}
		return string(jsonBytes), nil
	},
	"arrayToString": func(args ...interface{}) (interface{}, error) {
		// CRITICAL FIX: Handle empty array case gracefully
		// When govaluate encounters an empty array parameter, it calls the function with 0 arguments
		// This should return an empty string (which will be skipped by validation logic)
		if len(args) == 0 {
			return "", nil // Return empty string for empty arrays (will be skipped)
		}

		isLikelyFieldName := func(candidate string) bool {
			if candidate == "" {
				return false
			}
			for i := 0; i < len(candidate); i++ {
				ch := candidate[i]
				if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_' {
					continue
				}
				if i > 0 && ch >= '0' && ch <= '9' {
					continue
				}
				return false
			}
			return true
		}

		valueToString := func(v interface{}) string {
			switch val := v.(type) {
			case nil:
				return ""
			case string:
				return val
			case []byte:
				return string(val)
			case int:
				return strconv.Itoa(val)
			case int8:
				return strconv.FormatInt(int64(val), 10)
			case int16:
				return strconv.FormatInt(int64(val), 10)
			case int32:
				return strconv.FormatInt(int64(val), 10)
			case int64:
				return strconv.FormatInt(val, 10)
			case uint:
				return strconv.FormatUint(uint64(val), 10)
			case uint8:
				return strconv.FormatUint(uint64(val), 10)
			case uint16:
				return strconv.FormatUint(uint64(val), 10)
			case uint32:
				return strconv.FormatUint(uint64(val), 10)
			case uint64:
				return strconv.FormatUint(val, 10)
			case float32:
				return strconv.FormatFloat(float64(val), 'g', -1, 32)
			case float64:
				return strconv.FormatFloat(val, 'g', -1, 64)
			case bool:
				return strconv.FormatBool(val)
			default:
				return fmt.Sprint(v)
			}
		}

		// Optional: Support array/object field extraction
		// Usage: arrayToString(((result)), "uan")
		fieldName := ""
		if len(args) == 2 {
			if field, ok := args[1].(string); ok && isLikelyFieldName(field) {
				switch first := args[0].(type) {
				case []interface{}, map[string]interface{}:
					fieldName = field
				default:
					// Backward compatibility for missing-path calls like arrayToString("", "field").
					if first == nil || valueToString(first) == "" {
						return "", nil
					}
				}
			}
		}
		if len(args) > 2 {
			// Expanded object-array call shape:
			// arrayToString(obj1, obj2, ..., "fieldName")
			if field, ok := args[len(args)-1].(string); ok && isLikelyFieldName(field) {
				allMaps := true
				for i := 0; i < len(args)-1; i++ {
					if _, isMap := args[i].(map[string]interface{}); !isMap {
						allMaps = false
						break
					}
				}
				if allMaps {
					fieldName = field
					args = args[:len(args)-1]
				}
			}
		}

		// Handle expanded object-array calls with field extraction.
		if len(args) > 1 && fieldName != "" {
			allMaps := true
			for _, arg := range args {
				if _, ok := arg.(map[string]interface{}); !ok {
					allMaps = false
					break
				}
			}
			if !allMaps {
				// Let type-specific branches handle []interface{} / map cases below.
				goto expandedPrimitiveArgs
			}

			var builder strings.Builder
			estimatedSize := len(args) * 8
			builder.Grow(estimatedSize)
			for _, arg := range args {
				m, ok := arg.(map[string]interface{})
				if !ok {
					continue
				}
				v, exists := m[fieldName]
				if !exists || v == nil {
					continue
				}
				if builder.Len() > 0 {
					builder.WriteByte(',')
				}
				builder.WriteString(valueToString(v))
			}
			return builder.String(), nil
		}

		// IMPORTANT: govaluate can expand primitive arrays (e.g. []string)
		// into variadic args: arrayToString("a","b","c").
		// Join all expanded args directly so no element is dropped.
	expandedPrimitiveArgs:
		if len(args) > 1 && fieldName == "" {
			var builder strings.Builder
			estimatedSize := len(args) * 8
			builder.Grow(estimatedSize)
			builder.WriteString(valueToString(args[0]))
			for i := 1; i < len(args); i++ {
				builder.WriteByte(',')
				builder.WriteString(valueToString(args[i]))
			}
			return builder.String(), nil
		}

		// Single argument - handle different array types with optimized implementations
		switch arr := args[0].(type) {
		case map[string]interface{}:
			// Govaluate may extract a single object from an array and pass it as a map.
			// When field extraction is requested, return that field instead of stringifying the map.
			if fieldName != "" {
				v, exists := arr[fieldName]
				if !exists || v == nil {
					return "", nil
				}
				return valueToString(v), nil
			}
			return fmt.Sprint(arr), nil
		case []interface{}:
			if len(arr) == 0 {
				return "", nil
			}
			// Pre-allocate builder with estimated capacity
			var builder strings.Builder
			estimatedSize := len(arr) * 8 // Estimate 8 chars per item + comma
			builder.Grow(estimatedSize)

			wroteValue := false
			writeValue := func(v interface{}) {
				if wroteValue {
					builder.WriteByte(',')
				}
				builder.WriteString(valueToString(v))
				wroteValue = true
			}

			for _, item := range arr {
				if fieldName == "" {
					writeValue(item)
					continue
				}
				if m, ok := item.(map[string]interface{}); ok {
					if v, exists := m[fieldName]; exists {
						writeValue(v)
					}
				}
			}
			return builder.String(), nil

		case []string:
			// Most efficient path - direct join
			return strings.Join(arr, ","), nil

		case []int:
			if len(arr) == 0 {
				return "", nil
			}
			// Optimized integer conversion
			var builder strings.Builder
			estimatedSize := len(arr) * 6 // Estimate 6 chars per int + comma
			builder.Grow(estimatedSize)

			// First element
			builder.WriteString(strconv.Itoa(arr[0]))

			// Remaining elements
			for i := 1; i < len(arr); i++ {
				builder.WriteByte(',')
				builder.WriteString(strconv.Itoa(arr[i]))
			}
			return builder.String(), nil

		case []float64:
			if len(arr) == 0 {
				return "", nil
			}
			// Optimized float conversion
			var builder strings.Builder
			estimatedSize := len(arr) * 10 // Estimate 10 chars per float + comma
			builder.Grow(estimatedSize)

			// First element
			builder.WriteString(strconv.FormatFloat(arr[0], 'g', -1, 64))

			// Remaining elements
			for i := 1; i < len(arr); i++ {
				builder.WriteByte(',')
				builder.WriteString(strconv.FormatFloat(arr[i], 'g', -1, 64))
			}
			return builder.String(), nil

		case []int32:
			if len(arr) == 0 {
				return "", nil
			}
			var builder strings.Builder
			estimatedSize := len(arr) * 6
			builder.Grow(estimatedSize)

			builder.WriteString(strconv.FormatInt(int64(arr[0]), 10))
			for i := 1; i < len(arr); i++ {
				builder.WriteByte(',')
				builder.WriteString(strconv.FormatInt(int64(arr[i]), 10))
			}
			return builder.String(), nil

		case []int64:
			if len(arr) == 0 {
				return "", nil
			}
			var builder strings.Builder
			estimatedSize := len(arr) * 8
			builder.Grow(estimatedSize)

			builder.WriteString(strconv.FormatInt(arr[0], 10))
			for i := 1; i < len(arr); i++ {
				builder.WriteByte(',')
				builder.WriteString(strconv.FormatInt(arr[i], 10))
			}
			return builder.String(), nil

		case []float32:
			if len(arr) == 0 {
				return "", nil
			}
			var builder strings.Builder
			estimatedSize := len(arr) * 8
			builder.Grow(estimatedSize)

			builder.WriteString(strconv.FormatFloat(float64(arr[0]), 'g', -1, 32))
			for i := 1; i < len(arr); i++ {
				builder.WriteByte(',')
				builder.WriteString(strconv.FormatFloat(float64(arr[i]), 'g', -1, 32))
			}
			return builder.String(), nil

		case string:
			// CRITICAL FIX: Handle govaluate's single-element array extraction
			// When govaluate encounters a single-element array, it extracts the element
			// and passes it as a single string argument. We should return it as-is.
			return arr, nil

		default:
			// For non-array single values (numbers, etc.), convert to string
			// This handles cases where govaluate extracts single elements from arrays
			return valueToString(arr), nil
		}
	},
	"truncate": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return "", nil
		}
		value := args[0]
		if len(args) < 2 {
			return value, nil
		}
		// Backward compatibility: treat 3-arg truncate as raw slice mode.
		if len(args) >= 3 {
			start, okStart := expressionArgToInt(args[1])
			end, okEnd := expressionArgToInt(args[2])
			if !okStart || !okEnd {
				return fmt.Sprintf("%v", value), nil
			}
			return truncateRawStringValue(value, start, end), nil
		}
		maxChars, ok := expressionArgToInt(args[1])
		if !ok || maxChars < 0 {
			return value, nil
		}
		truncated, _ := truncateValuePreserveType(value, maxChars)
		return truncated, nil
	},
	"truncateRaw": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return "", nil
		}
		value := args[0]
		if len(args) < 2 {
			return fmt.Sprintf("%v", value), nil
		}
		start, okStart := expressionArgToInt(args[1])
		if !okStart {
			start = 0
		}
		if len(args) == 2 {
			return truncateRawStringValue(value, 0, start), nil
		}
		end, okEnd := expressionArgToInt(args[2])
		if !okEnd {
			return fmt.Sprintf("%v", value), nil
		}
		return truncateRawStringValue(value, start, end), nil
	},
	"jsonPart": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return "", nil
		}
		jsonPart, _ := splitJSONAndHTMLParts(args[0])
		return jsonPart, nil
	},
	"htmlPart": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return "", nil
		}
		_, htmlPart := splitJSONAndHTMLParts(args[0])
		return htmlPart, nil
	},
	// stringToJson parses a stringified JSON value (object or array) into a
	// Go map[string]interface{} / []interface{} tree. Streaming decode is
	// used so trailing content (e.g. an HTML report appended after the
	// JSON document) is tolerated without raising "Extra data" errors.
	//
	// Behaviour:
	//   - empty input          -> nil
	//   - already-parsed value -> passed through unchanged (idempotent)
	//   - invalid JSON         -> error
	//
	// Typical use sites:
	//   - preProcessing rules:  {{stringToJson(jsonPart(((CRIF.response.body.raw_response))))}}
	//   - body templates that need an object/array literal from a stringified field.
	"stringToJson": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return nil, nil
		}
		strVal, ok := args[0].(string)
		if !ok {
			return args[0], nil
		}
		if strVal == "" {
			return nil, nil
		}
		var result interface{}
		if err := jsonParser.NewDecoder(strings.NewReader(strVal)).Decode(&result); err != nil {
			return nil, fmt.Errorf("stringToJson: invalid JSON: %w", err)
		}
		return result, nil
	},
	// getPath walks an already-parsed object/array using a dot path. It is
	// the escape hatch for reaching INTO a "_pp*" preprocessed tree from
	// inside a govaluate expression, since "_pp*" keys are deliberately not
	// recursively flattened into the param map (see extractAllKeysEfficient).
	//
	// Example: {{toLower(getPath(_ppCrifRaw,'CIR-REPORT-FILE.HEADER-SEGMENT.STATUS'))}}
	//
	// Args:
	//   args[0] - the data tree (map/slice) to navigate
	//   args[1] - dot path string (supports "[index]" array access, no need
	//             to wrap in "((..))" - it is added internally)
	"getPath": func(args ...interface{}) (interface{}, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("getPath requires 2 args: data, path")
		}
		pathStr, ok := args[1].(string)
		if !ok {
			return nil, fmt.Errorf("getPath: path must be a string, got %T", args[1])
		}
		return getByDotPath(args[0], "(("+pathStr+"))"), nil
	},
	// findByField(array, fieldName, fieldValue [, extractField]) searches an
	// array of objects for the FIRST element where element[fieldName] ==
	// fieldValue (case-insensitive string comparison). If the optional fourth
	// argument extractField is given, that sub-field of the matched element is
	// returned instead of the whole element. Returns nil when no match is found.
	//
	// Primary use-case: CRIF DEMOGS.VARIATIONS is a flat array of typed
	// objects. To extract only ADDRESS-VARIATIONS entries:
	//   findByField(_ppCrifVariations, 'TYPE', 'ADDRESS-VARIATIONS', 'VARIATION')
	//
	// IMPORTANT — govaluate separator spreading: when the first argument is a
	// []interface{}, govaluate's separatorStage spreads its elements into the
	// args slice instead of passing the slice as a single value. The function
	// therefore supports two calling conventions (same as inListField):
	//   Normal path  : args = [[]interface{}{…}, fieldName, fieldValue (, extractField)]
	//   Expanded path: args = [map0, map1, …, mapN, fieldName, fieldValue (, extractField)]
	// The expanded path is detected when args[0] is a map rather than a slice.
	"findByField": func(args ...interface{}) (interface{}, error) {
		// findAndReturn is the shared search helper used by both paths.
		findAndReturn := func(elems []interface{}, fieldName, fieldValue, extractField string) interface{} {
			want := strings.ToLower(fieldValue)
			for _, elem := range elems {
				m, ok := elem.(map[string]interface{})
				if !ok {
					continue
				}
				if strings.ToLower(fmt.Sprintf("%v", m[fieldName])) == want {
					if extractField != "" {
						return m[extractField]
					}
					return m
				}
			}
			return nil
		}

		// Normal path: args[0] is the whole array (not spread by govaluate).
		if arr, ok := args[0].([]interface{}); ok {
			if len(args) < 3 || len(args) > 4 {
				return nil, fmt.Errorf("findByField requires 3 or 4 args: array, fieldName, fieldValue [, extractField]")
			}
			fieldName, ok := args[1].(string)
			if !ok {
				return nil, fmt.Errorf("findByField: fieldName must be a string, got %T", args[1])
			}
			fieldValue := fmt.Sprintf("%v", args[2])
			var extractField string
			if len(args) == 4 {
				if extractField, ok = args[3].(string); !ok {
					return nil, fmt.Errorf("findByField: extractField must be a string, got %T", args[3])
				}
			}
			return findAndReturn(arr, fieldName, fieldValue, extractField), nil
		}

		// Expanded path: govaluate spread the array elements into individual
		// map args. Trailing string args are fieldName, fieldValue[, extractField].
		// Scan from the end to locate the first string (extractField if 4-arg call,
		// or fieldValue if 3-arg call), then the next string is fieldValue / fieldName.
		if _, ok := args[0].(map[string]interface{}); ok {
			// Collect trailing string args (up to 3)
			var trailingStrings []string
			splitIdx := len(args)
			for i := len(args) - 1; i >= 0; i-- {
				if s, ok := args[i].(string); ok {
					trailingStrings = append([]string{s}, trailingStrings...)
					splitIdx = i
				} else {
					break
				}
			}
			if len(trailingStrings) < 2 || len(trailingStrings) > 3 {
				return nil, fmt.Errorf("findByField (expanded): expected 2 or 3 trailing string args (fieldName, fieldValue [,extractField]), got %d", len(trailingStrings))
			}
			fieldName := trailingStrings[0]
			fieldValue := trailingStrings[1]
			var extractField string
			if len(trailingStrings) == 3 {
				extractField = trailingStrings[2]
			}
			// The map elements before splitIdx are the spread array entries.
			elems := make([]interface{}, splitIdx)
			copy(elems, args[:splitIdx])
			return findAndReturn(elems, fieldName, fieldValue, extractField), nil
		}

		return nil, nil // non-array, non-map first arg → graceful nil
	},
	// selectByFieldSorted filters an array of objects by an (optional) nested
	// field == value, then returns a field from the element that WINS a sort on
	// another nested field. It is the generic "pick the latest / earliest /
	// max / min matching element and extract a value" helper.
	//
	// Call form:
	//   selectByFieldSorted(array, matchFieldPath, matchValue, sortFieldPath, order, extractFieldPath)
	//
	//   array            - []interface{} of objects, a single object, OR (because
	//                      govaluate spreads slice args into individual params)
	//                      the spread object elements. All three are handled.
	//   matchFieldPath   - dot path (supports "a.b", "a.b[0].c") to the filter
	//                      field; "" disables filtering.
	//   matchValue       - value matchFieldPath must equal (case-insensitive,
	//                      string comparison); "" disables filtering.
	//   sortFieldPath    - dot path to the sort key. Elements whose key is
	//                      null / empty are DROPPED (this yields "latest NOT-NULL"
	//                      semantics). Key type is auto-detected across the
	//                      surviving candidates: numeric if every key parses as a
	//                      number, else chronological if every key parses as a
	//                      date, else lexical string.
	//   order            - "asc" => winner is smallest/earliest; anything else
	//                      (default "desc") => winner is largest/latest.
	//   extractFieldPath - dot path returned from the winning element; "" returns
	//                      the whole element.
	//
	// Returns nil when no element matches / all sort keys are null, so a
	// downstream ternary can fall back (e.g. to uploadToS3). Ties keep the first
	// element in input order (stable).
	//
	// Example (latest CRIF consolidate PDF):
	//   selectByFieldSorted(((multibureau_consolidate_data__c.records)),
	//     'Multibureau__r.Bureau__c','CRIF','Multibureau_Date__c','desc',
	//     'Multibureau__r.Credit_Bureau_pdf__c')
	"selectByFieldSorted": func(args ...interface{}) (interface{}, error) {
		// The last 5 args are always the string params. Everything before them
		// is the element set: one []interface{} arg (non-spread), one map arg
		// (single object), or many map args (govaluate-spread slice).
		if len(args) < 5 {
			return nil, nil
		}
		params := args[len(args)-5:]
		matchFieldPath := expressionArgToString(params[0])
		matchValue := expressionArgToString(params[1])
		sortFieldPath := expressionArgToString(params[2])
		order := strings.ToLower(strings.TrimSpace(expressionArgToString(params[3])))
		extractFieldPath := expressionArgToString(params[4])

		leading := args[:len(args)-5]
		var elems []interface{}
		if len(leading) == 1 {
			if arr, ok := leading[0].([]interface{}); ok {
				elems = arr
			} else {
				elems = leading // single object (or scalar) treated as one element
			}
		} else {
			elems = leading // govaluate-spread object args (may be empty)
		}

		resolve := func(elem interface{}, path string) interface{} {
			if path == "" {
				return nil
			}
			return getByDotPath(elem, "(("+path+"))")
		}

		type candidate struct {
			elem interface{}
			key  string
		}
		filterActive := matchFieldPath != "" && matchValue != ""
		wantMatch := strings.ToLower(matchValue)

		var cands []candidate
		for _, e := range elems {
			if _, ok := e.(map[string]interface{}); !ok {
				continue // ignore non-object entries defensively
			}
			if filterActive {
				if strings.ToLower(expressionArgToString(resolve(e, matchFieldPath))) != wantMatch {
					continue
				}
			}
			raw := resolve(e, sortFieldPath)
			ks := expressionArgToString(raw)
			if raw == nil || strings.TrimSpace(ks) == "" {
				continue // drop null / empty sort keys
			}
			cands = append(cands, candidate{elem: e, key: ks})
		}
		if len(cands) == 0 {
			return nil, nil
		}

		// Detect a single comparison mode across all surviving keys.
		allFloat, allDate := true, true
		for _, c := range cands {
			if _, ok := expressionArgToFloat64(c.key); !ok {
				allFloat = false
			}
			if _, err := parseFlexibleDate(c.key); err != nil {
				allDate = false
			}
		}
		less := func(a, b string) bool { // is a < b under the chosen mode?
			if allFloat {
				fa, _ := expressionArgToFloat64(a)
				fb, _ := expressionArgToFloat64(b)
				return fa < fb
			}
			if allDate {
				ta, _ := parseFlexibleDate(a)
				tb, _ := parseFlexibleDate(b)
				return ta.Before(tb)
			}
			return a < b
		}

		asc := order == "asc"
		winner := cands[0]
		for _, c := range cands[1:] {
			var better bool
			if asc {
				better = less(c.key, winner.key) // smaller wins
			} else {
				better = less(winner.key, c.key) // larger wins
			}
			if better {
				winner = c
			}
		}

		if extractFieldPath == "" {
			return winner.elem, nil
		}
		return resolve(winner.elem, extractFieldPath), nil
	},
	"uploadToS3": func(args ...interface{}) (interface{}, error) {
		if len(args) < 5 {
			return "", fmt.Errorf("uploadToS3 requires 5 arguments: content, extension, filePath, bucketName, region")
		}
		content := expressionArgToString(args[0])
		extension := expressionArgToString(args[1])
		filePath := expressionArgToString(args[2])
		bucket := expressionArgToString(args[3])
		region := expressionArgToString(args[4])

		uploadedURL, err := uploadToS3WithConfig(content, extension, filePath, bucket, region)
		if err != nil {
			if commoninit.IsLoggerAvailable() {
				logLevelFn := commoninit.GetLogger().Errorw
				if strings.TrimSpace(content) == "" {
					logLevelFn = commoninit.GetLogger().Warnw
				}
				logLevelFn("uploadToS3 expression rejected/failed",
					"bucket", bucket,
					"region", region,
					"filePath", filePath,
					"extension", extension,
					"contentLength", len(content),
					"error", err)
			}
			return "", nil
		}
		return uploadedURL, nil
	},
	"now": func(args ...interface{}) (interface{}, error) {
		// Return current time in RFC3339Nano so it can be reliably parsed by formatDate(...)
		// Example usage in expressions: {{formatDate(now(), 'YYYY-MM-DD HH:mm:ss')}}
		if len(args) != 0 {
			return "", fmt.Errorf("now function does not accept any arguments")
		}
		return time.Now().UTC().Format(time.RFC3339Nano), nil
	},
	"formatDate": func(args ...interface{}) (interface{}, error) {
		if len(args) == 0 {
			return "", fmt.Errorf("formatDate function requires at least one argument")
		}

		// Convert first argument (input date) to string
		inputDateStr := strings.TrimSpace(fmt.Sprintf("%v", args[0]))

		// Default output format is DD/MM/YYYY if not specified
		outputFormat := "DD/MM/YYYY"
		if len(args) > 1 {
			outputFormat = strings.TrimSpace(fmt.Sprintf("%v", args[1]))
		}

		if inputDateStr == "" {
			return "", nil // Return empty for empty input
		}

		// Parse the input date using multiple possible formats
		parsedTime, err := parseFlexibleDate(inputDateStr)
		if err != nil {
			return "", fmt.Errorf("failed to parse date '%s': %v", inputDateStr, err)
		}

		// Format the parsed time according to the output format
		formattedDate, err := formatDateWithCustomFormat(parsedTime, outputFormat)
		if err != nil {
			return "", fmt.Errorf("failed to format date with format '%s': %v", outputFormat, err)
		}

		return formattedDate, nil
	},
}

var (
	dateInputFormats = []string{
		"2006-01-02T15:04:05.000Z0700",
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05Z0700",
		"2006-01-02T15:04:05-0700",
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		"2006-01-02",
		"02/01/2006",
		"01/02/2006",
		"2006/01/02",
		"2006/02/01",
		"01/2006/02",
		"02-01-2006",
		"01-02-2006",
		"2006-02-01",
		"2006_01_02",
		"2006_02_01",
		"02_01_2006",
		"01_02_2006",
		"20060102",
		"02012006",
		"01022006",
		"2006-01-02 03:04:05 PM",
		"02/01/2006 03:04:05 PM",
		"01/02/2006 03:04:05 PM",
		"January 2, 2006",
		"Jan 2, 2006",
		"2 January 2006",
		"2 Jan 2006",
	}
	dateFormatReplacer = strings.NewReplacer(
		"YYYY", "2006", "YY", "06",
		"MMMM", "January", "MMM", "Jan", "MM", "01", "M", "1",
		"DD", "02", "D", "2",
		"HH", "15", "H", "15",
		"hh", "03", "h", "3",
		"mm", "04", "m", "4",
		"ss", "05", "s", "5",
		"SSS", "000", "SS", "00", "S", "0",
		"A", "PM", "a", "pm",
		"ZZZ", "MST", "ZZ", "-0700", "Z", "Z0700",
	)
)

// parseFlexibleDate attempts to parse a date string using multiple common formats
func parseFlexibleDate(dateStr string) (time.Time, error) {
	for _, format := range dateInputFormats {
		if parsed, err := time.Parse(format, dateStr); err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse date string: %s", dateStr)
}

// formatDateWithCustomFormat formats a time.Time according to a custom format string
func formatDateWithCustomFormat(t time.Time, format string) (string, error) {
	goFormat := dateFormatReplacer.Replace(format)

	// Handle special case where time components are requested but not available in input
	// If the original time is at midnight (00:00:00), it likely means no time was specified in input
	if (strings.Contains(format, "HH") || strings.Contains(format, "hh") ||
		strings.Contains(format, "mm") || strings.Contains(format, "ss")) &&
		t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
		// Time components requested but input had no time - use 00:00:00
		// The time.Time already defaults to midnight, so just format it
	}

	return t.Format(goFormat), nil
}

type requestParameterCache struct {
	entries sync.Map
}

func newRequestParameterCache() *requestParameterCache {
	return &requestParameterCache{}
}

func getRequestScopedValueJSONCacheKey(valueJson map[string]interface{}) string {
	if valueJson == nil {
		return "nil"
	}
	// Cache only within the current request: map identity is enough and avoids expensive hashing.
	return fmt.Sprintf("%p", valueJson)
}

// Overlay contexts are created per array element and have near-zero reuse.
// Avoid retaining them in request cache to prevent request-time memory spikes.
func shouldBypassRequestScopedParamCache(valueJson map[string]interface{}) bool {
	if valueJson == nil {
		return true
	}
	_, hasRootOverlay := valueJson["__root"]
	_, hasCurrentOverlay := valueJson["current"]
	return hasRootOverlay || hasCurrentOverlay
}

func getRequestScopedParameters(paramsCache *requestParameterCache, valueJson map[string]interface{}) map[string]interface{} {
	if paramsCache == nil || shouldBypassRequestScopedParamCache(valueJson) {
		parameters := make(map[string]interface{}, 50)
		extractAllKeysEfficient(valueJson, "", parameters)
		return parameters
	}

	cacheKey := getRequestScopedValueJSONCacheKey(valueJson)
	if cached, found := paramsCache.entries.Load(cacheKey); found {
		if parameters, ok := cached.(map[string]interface{}); ok {
			return parameters
		}
	}

	parameters := make(map[string]interface{}, 50)
	extractAllKeysEfficient(valueJson, "", parameters)
	if actual, loaded := paramsCache.entries.LoadOrStore(cacheKey, parameters); loaded {
		if existing, ok := actual.(map[string]interface{}); ok {
			return existing
		}
	}
	return parameters
}

// Most efficient parameter extraction with intermediate object preservation
func extractAllKeysEfficient(data interface{}, prefix string, out map[string]interface{}) {
	switch val := data.(type) {
	case map[string]interface{}:
		// Store the intermediate object itself (for toJson functions)
		if prefix != "" {
			if needsConversion(prefix) {
				underscoreKey := convertToUnderscoreKey(prefix)
				out[underscoreKey] = val
			} else {
				out[prefix] = val
			}
		}

		// Continue with recursive extraction for leaf access
		for k, v := range val {
			if prefix == "" && k == "__root" {
				if rootMap, ok := v.(map[string]interface{}); ok {
					for rootKey, rootVal := range rootMap {
						// Defense in depth: keep the same "_pp*" shallow-store
						// behavior even when the caller hands us an overlay
						// map containing __root: valueJson. Without this,
						// any future call that flattens an overlay would
						// recursively explode preprocessed trees into the
						// param map, defeating the optimization below.
						if len(rootKey) > 3 && rootKey[0] == '_' && rootKey[1] == 'p' && rootKey[2] == 'p' {
							out[rootKey] = rootVal
							// govaluate-safe alias (see smartReplaceSpecialChars):
							// "_ppFoo" is rewritten in expressions to "pp__Foo"
							// because govaluate rejects "_"-leading identifiers.
							out["pp__"+rootKey[3:]] = rootVal
							continue
						}
						extractAllKeysEfficient(rootVal, rootKey, out)
					}
				}
				continue
			}
			// Preprocessing-injected keys (reserved "_pp" prefix): store the
			// top-level reference so getPath/serializeJson can resolve them,
			// but SKIP recursion. This avoids flattening potentially large
			// parsed trees (bureau reports can carry 500-2000 nested nodes)
			// into the govaluate param map on every interpolation.
			// Cost: 3-byte literal compare per top-level key (~ns). Saves
			// O(M) map inserts where M = number of nodes in the parsed tree.
			if prefix == "" && len(k) > 3 && k[0] == '_' && k[1] == 'p' && k[2] == 'p' {
				out[k] = v
				// Companion govaluate-safe alias; see smartReplaceSpecialChars.
				out["pp__"+k[3:]] = v
				continue
			}
			var newPrefix string
			if prefix == "" {
				newPrefix = k
			} else {
				// Use strings.Builder for efficient concatenation
				var builder strings.Builder
				builder.Grow(len(prefix) + len(k) + 1)
				builder.WriteString(prefix)
				builder.WriteByte('.')
				builder.WriteString(k)
				newPrefix = builder.String()
			}
			extractAllKeysEfficient(v, newPrefix, out)
		}
	case []interface{}:
		// Store the array itself (for toJson functions)
		if prefix != "" {
			if needsConversion(prefix) {
				underscoreKey := convertToUnderscoreKey(prefix)
				out[underscoreKey] = val
			} else {
				out[prefix] = val
			}
		}

		// Continue with recursive extraction for indexed access
		for i, v := range val {
			// Efficient array key building
			var builder strings.Builder
			builder.Grow(len(prefix) + 10) // Estimate for [index]
			builder.WriteString(prefix)
			builder.WriteByte('[')
			builder.WriteString(strconv.Itoa(i))
			builder.WriteByte(']')
			extractAllKeysEfficient(v, builder.String(), out)
		}
	default:
		// Single-pass key conversion to underscore format
		if needsConversion(prefix) {
			underscoreKey := convertToUnderscoreKey(prefix)
			out[underscoreKey] = val
		} else {
			out[prefix] = val
		}
	}
}

// Check if key needs conversion (faster than doing conversion always)
func needsConversion(key string) bool {
	return strings.ContainsAny(key, ".[ -") || strings.Contains(key, " ")
}

// shouldIncludeInBody returns true if v should be sent in the SF composite body (non-nil, non-blank).
// Used to avoid sending null or blank string values in both InterpolateValues and processArrayElement.
func shouldIncludeInBody(v interface{}) bool {
	if v == nil {
		return false
	}
	s, ok := v.(string)
	return !ok || s != ""
}

type preparedLookup struct {
	Alias       string
	Index       map[string]interface{}
	CurrentPath compiledLookupPath
}

type compiledLookupPath struct {
	Raw        string
	SingleKey  string
	Segments   []string
	UseGeneric bool
}

// normalizeLookupJoinKey trims and upper-cases keys so that joins remain stable
// across inconsistent casing/whitespace in upstream service payloads.
func normalizeLookupJoinKey(value interface{}) string {
	if value == nil {
		return ""
	}
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	case int:
		raw = strconv.Itoa(v)
	case int8:
		raw = strconv.FormatInt(int64(v), 10)
	case int16:
		raw = strconv.FormatInt(int64(v), 10)
	case int32:
		raw = strconv.FormatInt(int64(v), 10)
	case int64:
		raw = strconv.FormatInt(v, 10)
	case uint:
		raw = strconv.FormatUint(uint64(v), 10)
	case uint8:
		raw = strconv.FormatUint(uint64(v), 10)
	case uint16:
		raw = strconv.FormatUint(uint64(v), 10)
	case uint32:
		raw = strconv.FormatUint(uint64(v), 10)
	case uint64:
		raw = strconv.FormatUint(v, 10)
	case float32:
		raw = strconv.FormatFloat(float64(v), 'g', -1, 32)
	case float64:
		raw = strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		raw = strconv.FormatBool(v)
	default:
		raw = fmt.Sprint(v)
	}

	normalized := strings.ToUpper(strings.TrimSpace(raw))
	if normalized == "" || normalized == "<NIL>" {
		return ""
	}
	return normalized
}

func normalizeRelativePath(path string) string {
	clean := strings.TrimSpace(path)
	clean = strings.TrimPrefix(strings.TrimSuffix(clean, "))"), "((")
	return strings.TrimSpace(clean)
}

func compileLookupPath(path string) compiledLookupPath {
	cleanPath := normalizeRelativePath(path)
	if cleanPath == "" {
		return compiledLookupPath{}
	}

	// Use generic resolver only when brackets appear (e.g., a[0].b).
	if strings.Contains(cleanPath, "[") || strings.Contains(cleanPath, "]") {
		return compiledLookupPath{
			Raw:        cleanPath,
			UseGeneric: true,
		}
	}

	if !strings.Contains(cleanPath, ".") {
		return compiledLookupPath{
			Raw:       cleanPath,
			SingleKey: cleanPath,
		}
	}

	segments := strings.Split(cleanPath, ".")
	filtered := make([]string, 0, len(segments))
	for _, seg := range segments {
		trimmed := strings.TrimSpace(seg)
		if trimmed == "" {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	if len(filtered) == 0 {
		return compiledLookupPath{}
	}
	return compiledLookupPath{
		Raw:      cleanPath,
		Segments: filtered,
	}
}

func extractLookupPathValue(root interface{}, path compiledLookupPath) interface{} {
	if path.Raw == "" {
		return nil
	}

	if path.UseGeneric {
		return getByDotPath(map[string]interface{}{"root": root}, "((root."+path.Raw+"))")
	}

	currentMap, ok := root.(map[string]interface{})
	if !ok {
		return nil
	}

	if path.SingleKey != "" {
		return currentMap[path.SingleKey]
	}

	var curr interface{} = currentMap
	for _, seg := range path.Segments {
		nextMap, ok := curr.(map[string]interface{})
		if !ok {
			return nil
		}
		next, exists := nextMap[seg]
		if !exists {
			return nil
		}
		curr = next
	}
	return curr
}

func buildLookupIndex(fromArrayPath string, lookupKeyPath compiledLookupPath, valueJSON map[string]interface{}, log contracts.Logger) map[string]interface{} {
	lookupArrayData := getByDotPath(valueJSON, "(("+fromArrayPath+"))")
	lookupArr, ok := lookupArrayData.([]interface{})
	if !ok {
		if log != nil {
			log.Warnw("Lookup source array is invalid; disabling lookup for template", "fromArrayPath", fromArrayPath, "dataType", fmt.Sprintf("%T", lookupArrayData))
		}
		return nil
	}

	index := make(map[string]interface{}, len(lookupArr))
	duplicates := 0
	for _, item := range lookupArr {
		keyVal := extractLookupPathValue(item, lookupKeyPath)
		joinKey := normalizeLookupJoinKey(keyVal)
		if joinKey == "" {
			continue
		}
		if _, exists := index[joinKey]; exists {
			duplicates++
			continue
		}
		index[joinKey] = item
	}

	if duplicates > 0 && log != nil {
		log.Warnw("Duplicate lookup keys detected; keeping first occurrence", "duplicateCount", duplicates, "fromArrayPath", fromArrayPath, "lookupKeyPath", lookupKeyPath.Raw)
	}
	return index
}

func prepareTemplateLookup(templateLookup *LookupConfig, valueJSON map[string]interface{}, log contracts.Logger) *preparedLookup {
	if templateLookup == nil {
		return nil
	}

	alias := normalizeRelativePath(templateLookup.As)
	fromArrayPath := normalizeRelativePath(templateLookup.FromArrayPath)
	currentKeyPath := strings.TrimPrefix(normalizeRelativePath(templateLookup.CurrentKeyPath), "current.")
	lookupKeyPath := strings.TrimPrefix(normalizeRelativePath(templateLookup.LookupKeyPath), "current.")
	currentPath := compileLookupPath(currentKeyPath)
	lookupPath := compileLookupPath(lookupKeyPath)
	if alias == "" || fromArrayPath == "" || currentPath.Raw == "" || lookupPath.Raw == "" {
		if log != nil {
			log.Warnw("Lookup config is incomplete; disabling lookup for template", "alias", templateLookup.As, "fromArrayPath", templateLookup.FromArrayPath, "currentKeyPath", templateLookup.CurrentKeyPath, "lookupKeyPath", templateLookup.LookupKeyPath)
		}
		return nil
	}
	if strings.ContainsAny(alias, ".[ ]") {
		if log != nil {
			log.Warnw("Lookup alias contains unsupported characters; disabling lookup for template", "alias", alias)
		}
		return nil
	}

	index := buildLookupIndex(fromArrayPath, lookupPath, valueJSON, log)
	if len(index) == 0 {
		if log != nil {
			log.Warnw("Lookup index is empty; lookup alias will remain unresolved", "alias", alias, "fromArrayPath", fromArrayPath)
		}
		return nil
	}

	return &preparedLookup{
		Alias:       alias,
		Index:       index,
		CurrentPath: currentPath,
	}
}

// Efficient single-pass key conversion.
// Uses a stack-local strings.Builder because the keys are short (typically
// <100 bytes) and a sync.Pool for such tiny allocations adds more overhead
// than it saves.
func convertToUnderscoreKey(key string) string {
	if !needsConversion(key) {
		return key
	}

	var builder strings.Builder
	builder.Grow(len(key))
	for i := 0; i < len(key); i++ {
		switch key[i] {
		case '.', '[', '-', ' ':
			builder.WriteByte('_')
		case ']':
			// skip
		default:
			builder.WriteByte(key[i])
		}
	}
	return builder.String()
}

func formatUnresolvedField(index int, key, value string) string {
	var builder strings.Builder
	builder.Grow(10 + len(key) + len(value))
	builder.WriteByte('[')
	builder.WriteString(strconv.Itoa(index))
	builder.WriteString("] ")
	builder.WriteString(key)
	builder.WriteString(": ")
	builder.WriteString(value)
	return builder.String()
}

func formatFailedExpression(index int, key, value string, err error) string {
	errMsg := err.Error()
	var builder strings.Builder
	builder.Grow(20 + len(key) + len(value) + len(errMsg))

	builder.WriteByte('[')
	builder.WriteString(strconv.Itoa(index))
	builder.WriteString("] ")
	builder.WriteString(key)
	builder.WriteString(": ")
	builder.WriteString(value)
	builder.WriteString(" (error: ")
	builder.WriteString(errMsg)
	builder.WriteByte(')')

	return builder.String()
}

// Smart character replacement that preserves special characters inside quoted strings
// isGovaluateIdentChar reports whether ch is a govaluate-identifier-extension
// character (letter, digit, or underscore). govaluate REQUIRES the first
// character of a bare identifier to be a letter -- a leading underscore
// produces an "Invalid token: '_'" parse error.
func isGovaluateIdentChar(ch byte) bool {
	return ch == '_' ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9')
}

// smartReplaceSpecialChars converts a raw template expression into a
// govaluate-parseable form by:
//
//   - replacing dots, hyphens, spaces, and "[" with "_" so dotted paths
//     map to flat parameter keys (e.g. "((a.b))" -> "((a_b))");
//   - dropping "]" so bracket-indexed paths collapse to identifiers;
//   - preserving all characters inside single- or double-quoted string
//     literals (so user-supplied paths like 'CIR-REPORT-FILE.X' survive); and
//   - REWRITING the reserved "_pp" identifier prefix to "pp__" at every
//     identifier-start position. govaluate's lexer rejects identifiers that
//     start with "_", so the bare "_ppFoo" identifier we use for
//     preprocessed values would parse as an invalid "_" token. The
//     companion alias in extractAllKeysEfficient guarantees the rewritten
//     name resolves to the same parameter value.
func smartReplaceSpecialChars(expr string) string {
	var result strings.Builder
	// Worst case: every "_pp" expands by 1 byte. A modest over-allocation
	// keeps Grow O(1) and avoids reallocs for typical expressions.
	result.Grow(len(expr) + 8)

	inQuotes := false
	quoteChar := byte(0)

	for i := 0; i < len(expr); i++ {
		ch := expr[i]

		// Quote toggling (respect backslash escapes).
		if (ch == '"' || ch == '\'') && (i == 0 || expr[i-1] != '\\') {
			if !inQuotes {
				inQuotes = true
				quoteChar = ch
			} else if ch == quoteChar {
				inQuotes = false
				quoteChar = 0
			}
			result.WriteByte(ch)
			continue
		}

		if inQuotes {
			result.WriteByte(ch)
			continue
		}

		// "_pp" rewrite at identifier-start positions. We judge "identifier
		// start" from the INPUT side: the preceding *input* char is not a
		// govaluate identifier-extension char (letter / digit / "_"). This
		// keeps the rewrite semantically correct even when an adjacent
		// special char (space, dot, ...) is being collapsed to "_" in the
		// output: in the original expression those chars terminate the
		// previous identifier, so the following "_pp" starts a fresh one.
		// Lookahead enforces a non-empty identifier after "_pp" so the
		// bare "_pp" sequence (already rejected by the storeAs validator)
		// is left alone.
		prevIsIdent := i > 0 && isGovaluateIdentChar(expr[i-1])
		if ch == '_' && !prevIsIdent &&
			i+3 < len(expr) &&
			expr[i+1] == 'p' && expr[i+2] == 'p' &&
			isGovaluateIdentChar(expr[i+3]) {
			result.WriteString("pp__")
			i += 2 // skip the two 'p's; outer loop's i++ steps past '_'
			continue
		}

		switch ch {
		case '.', '-', '[':
			result.WriteByte('_')
		case ' ':
			// Spaces are ambiguous: they may either JOIN identifier-extension
			// chars (e.g. "current.first name" -> "current_first_name") or
			// be purely cosmetic whitespace between operator-separated
			// tokens (e.g. "getPath(a, b)" -> "getPath(a,b)"). Emitting "_"
			// unconditionally produced orphan underscores like "f(a,_b)"
			// which govaluate's lexer rejects as "Invalid token". Decide
			// by looking at BOTH the previous and next input bytes: only
			// emit "_" when each side either is, or will become, part of
			// a govaluate identifier (letter/digit/"_", or one of ".", "-",
			// "[" which converts to "_"). Otherwise drop the space.
			isJoinable := func(b byte) bool {
				return isGovaluateIdentChar(b) || b == '.' || b == '-' || b == '['
			}
			if i > 0 && i+1 < len(expr) && isJoinable(expr[i-1]) && isJoinable(expr[i+1]) {
				result.WriteByte('_')
			}
		case ']':
		default:
			result.WriteByte(ch)
		}
	}

	return result.String()
}

// Updated expression evaluation with caching.
// When skipReplaceSpecialChars is true (e.g. filter expressions already resolved to literals),
// the expression is used as-is so spaces and operators are not mangled (e.g. 'Active' == 'Active').
// log is optional; when non-nil it is used for error logging (request-context enriched). When nil, commoninit.GetLogger() is used.
func getOrParseEvaluableExpression(cleanExpr string, skipReplaceSpecialChars bool, log contracts.Logger) (*govaluate.EvaluableExpression, string, error) {
	if log == nil {
		log = commoninit.GetLogger()
	}
	var govaluateExpr string
	if skipReplaceSpecialChars {
		govaluateExpr = strings.TrimSpace(cleanExpr)
	} else {
		// Convert expression for govaluate with smart replacements (dots/spaces to underscore for param lookup)
		govaluateExpr = smartReplaceSpecialChars(cleanExpr)
	}

	cache := getExprCache()
	parsedExpr, found := cache.Load(govaluateExpr)
	if found {
		if entry, ok := parsedExpr.(exprCacheEntry); ok {
			if cacheEntryTTL > 0 && time.Now().UnixNano() > entry.expiresAt {
				// Only decrement the counter if we're still operating on the current cache.
				// If a reset happened concurrently, the old map is orphaned and the counter
				// already belongs to the new map -- decrementing it would make it negative.
				if _, loaded := cache.LoadAndDelete(govaluateExpr); loaded {
					if cache == getExprCache() {
						exprCacheEntryCount.Add(-1)
					}
				}
				found = false
			} else {
				parsedExpr = entry
			}
		} else {
			found = false
		}
	}
	if !found {
		e, err := govaluate.NewEvaluableExpressionWithFunctions(govaluateExpr, customFunctions)
		if err != nil {
			log.Error(fmt.Sprintf("Failed to parse expression: %s, error: %v", cleanExpr, err))
			return nil, govaluateExpr, err
		}
		loadDynamicCacheLimits()
		if exprCacheMaxEntries > 0 && exprCacheEntryCount.Load() >= exprCacheMaxEntries {
			resetExprCache("max_entries_reached")
		}
		// Always re-fetch the cache pointer after a potential reset to ensure we
		// store into the current (possibly new) map, not the old orphaned one.
		cache = getExprCache()
		entry := exprCacheEntry{
			expr:      e,
			expiresAt: time.Now().Add(cacheEntryTTL).UnixNano(),
		}
		if actual, loaded := cache.LoadOrStore(govaluateExpr, entry); loaded {
			if existing, ok := actual.(exprCacheEntry); ok {
				parsedExpr = existing
			} else {
				cache.Store(govaluateExpr, entry)
				parsedExpr = entry
			}
		} else {
			exprCacheEntryCount.Add(1)
			parsedExpr = entry
		}
	}
	return parsedExpr.(exprCacheEntry).expr, govaluateExpr, nil
}

type layeredExpressionParameters struct {
	base    map[string]interface{}
	overlay map[string]interface{}
}

func (p layeredExpressionParameters) Get(name string) (interface{}, error) {
	if p.overlay != nil {
		if val, exists := p.overlay[name]; exists {
			return val, nil
		}
	}
	if p.base != nil {
		if val, exists := p.base[name]; exists {
			return val, nil
		}
	}
	// Preserve existing behavior where missing vars are treated as empty string.
	return "", nil
}

func evaluateExpressionWithParameters(expr string, baseParameters map[string]interface{}, overlayParameters map[string]interface{}, skipReplaceSpecialChars bool, log contracts.Logger) (interface{}, error) {
	if log == nil {
		log = commoninit.GetLogger()
	}
	// Strip the curly braces before evaluation
	cleanExpr := strings.TrimPrefix(strings.TrimSuffix(expr, "}}"), "{{")
	e, _, err := getOrParseEvaluableExpression(cleanExpr, skipReplaceSpecialChars, log)
	if err != nil {
		return nil, err
	}

	layeredParams := layeredExpressionParameters{
		base:    baseParameters,
		overlay: overlayParameters,
	}
	// Fast path for literal-only expressions to avoid any parameter lookups.
	if len(e.Vars()) == 0 {
		layeredParams = layeredExpressionParameters{}
	}
	result, err := e.Eval(layeredParams)
	if err != nil {
		if _, isMissingParam := err.(*MissingParameterError); !isMissingParam {
			log.Error(fmt.Sprintf("Expression evaluation failed for: %s, error: %v", cleanExpr, err))
		}
		return nil, &MissingParameterError{err}
	}
	return result, nil
}

func evaluateExpression(expr string, valueJson map[string]interface{}, skipReplaceSpecialChars bool, log contracts.Logger, paramsCache *requestParameterCache) (interface{}, error) {
	if log == nil {
		log = commoninit.GetLogger()
	}
	// Strip the curly braces before evaluation
	cleanExpr := strings.TrimPrefix(strings.TrimSuffix(expr, "}}"), "{{")

	var govaluateExpr string
	if skipReplaceSpecialChars {
		govaluateExpr = strings.TrimSpace(cleanExpr)
	} else {
		// Convert expression for govaluate with smart replacements (dots/spaces to underscore for param lookup)
		govaluateExpr = smartReplaceSpecialChars(cleanExpr)
	}

	cache := getExprCache()
	parsedExpr, found := cache.Load(govaluateExpr)
	if found {
		if entry, ok := parsedExpr.(exprCacheEntry); ok {
			if cacheEntryTTL > 0 && time.Now().UnixNano() > entry.expiresAt {
				// Only decrement the counter if we're still operating on the current cache.
				// If a reset happened concurrently, the old map is orphaned and the counter
				// already belongs to the new map -- decrementing it would make it negative.
				if _, loaded := cache.LoadAndDelete(govaluateExpr); loaded {
					if cache == getExprCache() {
						exprCacheEntryCount.Add(-1)
					}
				}
				found = false
			} else {
				parsedExpr = entry
			}
		} else {
			found = false
		}
	}
	if !found {
		e, err := govaluate.NewEvaluableExpressionWithFunctions(govaluateExpr, customFunctions)
		if err != nil {
			log.Error(fmt.Sprintf("Failed to parse expression: %s, error: %v", cleanExpr, err))
			return nil, err
		}
		loadDynamicCacheLimits()
		if exprCacheMaxEntries > 0 && exprCacheEntryCount.Load() >= exprCacheMaxEntries {
			resetExprCache("max_entries_reached")
		}
		// Always re-fetch the cache pointer after a potential reset to ensure we
		// store into the current (possibly new) map, not the old orphaned one.
		cache = getExprCache()
		entry := exprCacheEntry{
			expr:      e,
			expiresAt: time.Now().Add(cacheEntryTTL).UnixNano(),
		}
		if actual, loaded := cache.LoadOrStore(govaluateExpr, entry); loaded {
			if existing, ok := actual.(exprCacheEntry); ok {
				parsedExpr = existing
			} else {
				cache.Store(govaluateExpr, entry)
				parsedExpr = entry
			}
		} else {
			exprCacheEntryCount.Add(1)
			parsedExpr = entry
		}
	}
	e := parsedExpr.(exprCacheEntry).expr

	baseParameters := getRequestScopedParameters(paramsCache, valueJson)

	// Make a per-evaluation copy so we can safely inject missing variables.
	// Pre-size for base + expression vars (vars can include duplicates; over-allocation is fine).
	parameters := make(map[string]interface{}, len(baseParameters)+len(e.Vars()))
	for k, v := range baseParameters {
		parameters[k] = v
	}

	// Inject empty string for any variable referenced in the expression but missing from parameters
	// (e.g. SF omits keys for null/empty fields; dynamic per expression so no static config list needed)
	for _, v := range e.Vars() {
		if _, exists := parameters[v]; !exists {
			parameters[v] = ""
		}
	}

	result, err := e.Evaluate(parameters)
	if err != nil {
		// Return a special sentinel value for missing parameters
		if _, isMissingParam := err.(*MissingParameterError); !isMissingParam {
			log.Error(fmt.Sprintf("Expression evaluation failed for: %s, error: %v", cleanExpr, err))
		}
		return nil, &MissingParameterError{err}
	}
	return result, nil
}

// Optional: Add cache cleanup for memory management
func ClearParameterCache() {
	commoninit.GetLogger().Info("Global parameter cache removed; request-scoped cache is auto-cleared per request")
}

// Main transformer function. ctx is optional; when present and containing a request logger, that logger is used for correlation_id etc.
func (updater *DynamicJsonUpdater) InterpolateValues(ctx context.Context, sfMapping []types.SFRequest, valueJson map[string]interface{}) ([]types.SFRequest, error) {
	log := getRequestLogger(ctx, updater.log)
	start := time.Now()
	log.Info("InterpolateValues started - processing templates: ", len(sfMapping))

	var wg sync.WaitGroup
	transformed := make([]types.SFRequest, len(sfMapping))
	errorBufferSize := len(sfMapping) * 4
	if errorBufferSize < 1 {
		errorBufferSize = 1
	}
	errors := make(chan error, errorBufferSize)
	paramsCache := newRequestParameterCache()
	sendErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case errors <- err:
		default:
			log.Warnw("Dropping interpolation error due to full error channel", "error", err)
		}
	}

	maxPar := updater.MaxParallelGoroutines
	if maxPar < 1 {
		maxPar = 0
	}
	var sem chan struct{}
	if maxPar > 0 {
		sem = make(chan struct{}, maxPar)
	}
	globalSem := getGlobalWorkerSemaphore()
	for i, item := range sfMapping {
		// Acquire the broader semaphore first, then the narrower one.
		// This prevents priority inversion: a goroutine holding a local
		// slot while blocking on the global semaphore would starve other
		// requests competing for local slots.
		if globalSem != nil {
			globalSem <- struct{}{}
		}
		if sem != nil {
			sem <- struct{}{}
		}
		wg.Add(1)
		go func(i int, item types.SFRequest) {
			defer wg.Done()
			if sem != nil {
				defer func() { <-sem }()
			}
			if globalSem != nil {
				defer func() { <-globalSem }()
			}
			defer func() {
				if r := recover(); r != nil {
					panicErr := fmt.Errorf("panic in InterpolateValues template goroutine (templateIndex=%d): %v", i, r)
					log.Errorw("Recovered panic in InterpolateValues template goroutine",
						"templateIndex", i,
						"panic", r,
						"stackTrace", string(debug.Stack()))
					sendErr(panicErr)
				}
			}()
			newBody := make(map[string]interface{})

			for key, rawVal := range item.Body {
				valStr, ok := rawVal.(string)
				if !ok {
					// Quick null check for non-strings
					if rawVal != nil {
						newBody[key] = rawVal
					}
					continue
				}

				if isExpression(valStr) {
					res, err := evaluateExpression(valStr, valueJson, false, log, paramsCache)
					if err != nil {
						// Omit key on error so SF composite request does not receive null/erroneous values.
						if _, isMissingParam := err.(*MissingParameterError); isMissingParam {
							log.Debugw("Skipping missing parameter in template: ", i, " key: ", key, " expression: ", valStr)
						} else {
							log.Errorw("Expression evaluation failed in template", "templateIndex", i, "key", key, "error", err)
							sendErr(fmt.Errorf("error in key %s: %v", key, err))
						}
						continue
					}
					// Debug logging for expression resolution
					log.Debugw("Regular template expression resolution", "key", key, "expression", valStr, "resolvedValue", res, "valueType", fmt.Sprintf("%T", res))
					// Include the key only for non-null, non-blank values; omit null and blank so SF does not receive them.
					if shouldIncludeInBody(res) {
						newBody[key] = res
					}
				} else if isDotPath(valStr) {
					val := getByDotPath(valueJson, valStr)
					// Debug logging for path resolution
					log.Debugw("Regular template path resolution", "key", key, "path", valStr, "resolvedValue", val, "valueType", fmt.Sprintf("%T", val))
					if shouldIncludeInBody(val) {
						newBody[key] = val
					}
				} else if isSFVariable(valStr) {
					// NEW: Handle <Variable> syntax in body fields
					val := substituteSFVariables(valStr, valueJson)
					if val != valStr && val != "" {
						newBody[key] = val
					}
				} else {
					// Hardcoded strings - quick empty check
					if valStr != "" {
						newBody[key] = valStr
					}
				}
			}

			item.Body = newBody
			item.URL = substituteSFVariables(item.URL, valueJson)

			transformed[i] = item
		}(i, item)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			log.Errorw("InterpolateValues failed", "error", err)
			// return nil, err
		}
	}

	duration := time.Since(start)
	log.Info("InterpolateValues completed in: ", duration, " for templates: ", len(sfMapping))
	return transformed, nil
}

// ppRefPattern matches "_pp*" references inside preProcessing expressions in
// either form:
//   - path form:        ((_ppCrifRaw.X.Y))
//   - identifier form:  getPath(_ppCrifRaw, 'X.Y')
//
// The non-capturing prefix `(?:^|[^A-Za-z0-9_])` enforces a word boundary so
// substrings inside an unrelated identifier (e.g. `foo_pp`) are NOT matched.
// This is critical because evaluateExpression auto-injects an empty string
// for any govaluate Var not found in the param map, which would otherwise
// MASK a real MissingParameterError for an unresolved "_pp*" dependency and
// cause the rule to silently produce wrong output. By detecting both forms
// up front we defer the rule to the next pass instead.
// Compiled once at package init.
var ppRefPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(_pp[A-Za-z0-9_-]+)`)

// ApplyPreprocessings runs every preProcessing rule collected from the
// merged SFRequestDB list, mutating valueJson in place with the produced
// values keyed by storeAs.
//
// Design contract:
//   - storeAs MUST start with the reserved "_pp" prefix (e.g. "_ppCrifRaw").
//     extractAllKeysEfficient relies on this prefix to skip recursive
//     flattening of preprocessed trees into the govaluate param map.
//   - Rules are deduplicated by storeAs across all requests; the first
//     declaration in iteration order wins. Sibling SF requests can therefore
//     declare the same rule redundantly without paying any extra cost.
//   - Original ESA data in valueJson is never overwritten; preprocessing
//     produces NEW top-level keys under "_pp*".
//
// Ordering / fixpoint algorithm:
//   - A rule whose expression references a "_pp*" key not yet present in
//     valueJson is "deferred" to the next pass (via cheap regex pre-scan)
//     or via MissingParameterError from govaluate.
//   - We loop until either every rule has been applied or a full pass made
//     zero progress (cycle or genuinely missing dependency), in which case
//     we surface a clear error naming the unresolved rules.
//   - Typical happy path is a single pass when rules are declared in their
//     natural dependency order; misordering still resolves automatically.
//
// Complexity:
//   - O(P * R) time where P = pass count (1 for well-ordered rules; bounded
//     by dependency-chain depth) and R = deduplicated rule count.
//   - O(R) extra space for the pending/deferred slices + storeAs dedup set.
//
// Failure policy (graceful degradation):
//   - A single rule failure (bad expression, custom-function error, malformed
//     JSON, etc.) is ISOLATED: the failing rule's storeAs is set to nil in
//     valueJson, the underlying error is logged with full context, and the
//     remaining rules + downstream SF requests continue executing. Storing
//     nil (rather than skipping the key) prevents dependent rules from
//     deferring forever and lets downstream "((_pp*))" lookups resolve
//     cleanly to empty.
//   - Per-rule config bugs (invalid storeAs prefix, empty expr) are also
//     isolated: log + skip the bad rule, never abort the request.
//   - Cycles and truly-unproduced "_pp*" dependencies are isolated: every
//     stuck rule is logged with its missing deps and nil-stored; the overall
//     call still returns nil so the rest of the pipeline runs.
//   - The ONLY case that aborts is caller-driven context cancellation, which
//     is not a rule-level concern.
//
// Concurrency: caller-owned valueJson; this function is synchronous and
// MUST NOT be invoked concurrently for the same valueJson instance.
func (updater *DynamicJsonUpdater) ApplyPreprocessings(ctx context.Context, valueJson map[string]interface{}, requests []types.SFRequestDB) (*types.PreprocessingSummary, error) {
	log := getRequestLogger(ctx, updater.log)
	start := time.Now()
	summary := &types.PreprocessingSummary{}
	if valueJson == nil {
		summary.DurationMs = time.Since(start).Milliseconds()
		return summary, nil
	}

	// Phase 1: collect unique rules in declaration order. First storeAs wins;
	// duplicates from sibling requests are silently dropped at this layer.
	// Malformed rules are logged and skipped (graceful degradation).
	// Pre-sized assuming a small handful of rules per request set.
	seen := make(map[string]struct{}, 4)
	pending := make([]types.PreprocessingConfig, 0, 4)
	for i := range requests {
		for _, pp := range requests[i].PreProcessing {
			// Enforce reserved namespace so the flat-param skip in
			// extractAllKeysEfficient stays correct by construction. Require
			// at least one identifier char after the "_pp" prefix so the
			// bare "_pp" key is rejected (it offers no useful semantics).
			if len(pp.StoreAs) <= 3 || !strings.HasPrefix(pp.StoreAs, "_pp") {
				log.Errorw("preProcessing: skipping rule with invalid storeAs (must start with reserved \"_pp\" prefix and have at least one char after)",
					"storeAs", pp.StoreAs)
				summary.SkippedInvalid++
				summary.Rules = append(summary.Rules, types.PreprocessingRuleResult{
					StoreAs: pp.StoreAs,
					Expr:    pp.Expr,
					Status:  types.PreprocessingRuleStatusSkipped,
					Error:   "invalid storeAs (must start with reserved \"_pp\" prefix and have at least one char after)",
				})
				continue
			}
			if strings.TrimSpace(pp.Expr) == "" {
				log.Errorw("preProcessing: skipping rule with empty expr",
					"storeAs", pp.StoreAs)
				summary.SkippedInvalid++
				summary.Rules = append(summary.Rules, types.PreprocessingRuleResult{
					StoreAs: pp.StoreAs,
					Status:  types.PreprocessingRuleStatusSkipped,
					Error:   "empty expr",
				})
				continue
			}
			if _, dup := seen[pp.StoreAs]; dup {
				summary.Duplicates++
				continue
			}
			seen[pp.StoreAs] = struct{}{}
			pending = append(pending, pp)
		}
	}
	if len(pending) == 0 {
		summary.DurationMs = time.Since(start).Milliseconds()
		// Only emit an Info line when configs were present but all got
		// filtered out -- otherwise stay silent to keep the no-op fast path
		// noise-free for the common "no preprocessing configured" case.
		if summary.SkippedInvalid > 0 || summary.Duplicates > 0 {
			log.Infow("ApplyPreprocessings: nothing to run",
				"skippedInvalid", summary.SkippedInvalid,
				"duplicates", summary.Duplicates,
				"duration_ms", summary.DurationMs)
		}
		return summary, nil
	}

	summary.RuleCount = len(pending)
	log.Infow("ApplyPreprocessings: starting",
		"ruleCount", summary.RuleCount,
		"skippedInvalid", summary.SkippedInvalid,
		"duplicates", summary.Duplicates)

	// Phase 2: multi-pass fixpoint. Each pass evaluates whatever rules whose
	// "_pp*" dependencies (if any) are already resolved. Rules that still
	// need an unresolved dependency are deferred to the next pass.
	for {
		summary.Passes++
		// Honor caller cancellation between passes. Rules may be expensive
		// (large JSON parses); avoid burning CPU on a request the client
		// has already abandoned. This is the ONLY hard-failure path -- it
		// is caller-driven and not a rule-level concern.
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				summary.DurationMs = time.Since(start).Milliseconds()
				summary.Cancelled = true
				log.Warnw("ApplyPreprocessings: cancelled mid-flight",
					"pass", summary.Passes,
					"ruleCount", summary.RuleCount,
					"succeeded", summary.Succeeded,
					"failed", summary.Failed,
					"duration_ms", summary.DurationMs,
					"error", err)
				return summary, err
			}
		}
		var deferred []types.PreprocessingConfig
		progressed := false
		for _, pp := range pending {
			// Cheap regex pre-scan: if expr references a "_pp*" key that is
			// not yet in valueJson, defer without invoking govaluate at all.
			// This avoids cache-warming the parser for a rule we know cannot
			// succeed yet and keeps error logs tidy.
			if pendingPreprocessingDependency(pp.Expr, valueJson) {
				log.Infow("preProcessing rule deferred to next pass (unmet _pp* dependency)",
					"pass", summary.Passes,
					"storeAs", pp.StoreAs,
					"missingPpKeys", collectMissingPpRefs(pp.Expr, valueJson))
				deferred = append(deferred, pp)
				continue
			}
			// paramsCache=nil so each evaluation rebuilds the flat param map
			// fresh. This is required for chained rules because earlier rules
			// have already mutated valueJson; reusing a stale cache would
			// hide newly-injected "_pp*" references.
			//
			// Note on errors: evaluateExpression wraps EVERY govaluate error
			// in *MissingParameterError (even parse / type / function errors)
			// because it also auto-injects "" for any govaluate Var missing
			// from the param map. That auto-injection means we can NEVER get
			// a genuine missing-parameter error here once the regex pre-scan
			// above has cleared `_pp*` dependencies. Any err we see now is
			// therefore a real failure (bad expr, custom-func error, etc.).
			//
			// Failure policy: isolate -- log the error with full context,
			// store nil under storeAs (so dependent rules don't defer
			// indefinitely and downstream "((_pp*))" lookups resolve to
			// empty), and continue.
			ruleStart := time.Now()
			result, err := evaluateExpression(pp.Expr, valueJson, false, log, nil)
			ruleDurationMs := time.Since(ruleStart).Milliseconds()
			if err != nil {
				rootErr := err
				if mpe, ok := err.(*MissingParameterError); ok && mpe != nil && mpe.Err != nil {
					rootErr = mpe.Err
				}
				log.Errorw("preProcessing rule failed; storing nil and continuing",
					"pass", summary.Passes,
					"storeAs", pp.StoreAs,
					"expr", pp.Expr,
					"duration_ms", ruleDurationMs,
					"error", rootErr)
				valueJson[pp.StoreAs] = nil
				summary.Failed++
				summary.Rules = append(summary.Rules, types.PreprocessingRuleResult{
					StoreAs:    pp.StoreAs,
					Expr:       pp.Expr,
					Status:     types.PreprocessingRuleStatusFailed,
					Pass:       summary.Passes,
					DurationMs: ruleDurationMs,
					Error:      rootErr.Error(),
				})
				progressed = true
				continue
			}
			valueJson[pp.StoreAs] = result
			summary.Succeeded++
			summary.Rules = append(summary.Rules, types.PreprocessingRuleResult{
				StoreAs:    pp.StoreAs,
				Expr:       pp.Expr,
				Status:     types.PreprocessingRuleStatusSuccess,
				Pass:       summary.Passes,
				DurationMs: ruleDurationMs,
			})
			progressed = true
			log.Infow("preProcessing rule applied",
				"pass", summary.Passes,
				"storeAs", pp.StoreAs,
				"resultType", fmt.Sprintf("%T", result),
				"duration_ms", ruleDurationMs)
		}

		if len(deferred) == 0 {
			summary.DurationMs = time.Since(start).Milliseconds()
			summary.Values = collectPpValues(valueJson)
			log.Infow("ApplyPreprocessings: completed",
				"ruleCount", summary.RuleCount,
				"succeeded", summary.Succeeded,
				"failed", summary.Failed,
				"skippedInvalid", summary.SkippedInvalid,
				"passes", summary.Passes,
				"duration_ms", summary.DurationMs)
			return summary, nil
		}
		if !progressed {
			// Fixpoint stalled: cycle or truly-unproduced "_pp*" dependency.
			// Isolate per the failure policy -- log each stuck rule with the
			// specific missing keys it was waiting on, nil-store it so
			// downstream lookups don't error, and return nil so the rest of
			// the pipeline (other SF requests, body interpolation, array
			// expansion) keeps running on whatever data IS available.
			for _, pp := range deferred {
				missing := collectMissingPpRefs(pp.Expr, valueJson)
				log.Errorw("preProcessing rule unresolved (cycle or missing _pp* dependency); storing nil and continuing",
					"pass", summary.Passes,
					"storeAs", pp.StoreAs,
					"expr", pp.Expr,
					"missingPpKeys", missing)
				valueJson[pp.StoreAs] = nil
				summary.Unresolved++
				summary.Rules = append(summary.Rules, types.PreprocessingRuleResult{
					StoreAs:       pp.StoreAs,
					Expr:          pp.Expr,
					Status:        types.PreprocessingRuleStatusUnresolved,
					Pass:          summary.Passes,
					Error:         "cycle or missing _pp* dependency",
					MissingPpKeys: missing,
				})
			}
			summary.DurationMs = time.Since(start).Milliseconds()
			summary.Values = collectPpValues(valueJson)
			log.Warnw("ApplyPreprocessings: completed with unresolved rules",
				"ruleCount", summary.RuleCount,
				"succeeded", summary.Succeeded,
				"failed", summary.Failed,
				"unresolvedCount", summary.Unresolved,
				"skippedInvalid", summary.SkippedInvalid,
				"passes", summary.Passes,
				"duration_ms", summary.DurationMs)
			return summary, nil
		}
		pending = deferred
	}
}

// collectPpValues snapshots every top-level "_pp*" key from valueJson into a
// new map (shallow copy: references are reused). Used to build the audit
// trail persisted in the decision-manager mongo log so operators can replay
// exactly what preprocessing produced. Lives next to ApplyPreprocessings so
// the prefix policy stays in one file.
func collectPpValues(valueJson map[string]interface{}) map[string]interface{} {
	if valueJson == nil {
		return nil
	}
	out := make(map[string]interface{}, 4)
	for k, v := range valueJson {
		if len(k) >= 3 && k[0] == '_' && k[1] == 'p' && k[2] == 'p' {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// pendingPreprocessingDependency returns true when expr references at least
// one top-level "_pp*" key that is not yet present in valueJson. Cheap
// regex scan reusing the precompiled ppRefPattern; only the top-level
// segment of any dotted reference is inspected (e.g. "((_ppCrifRaw.X.Y))"
// -> key "_ppCrifRaw").
func pendingPreprocessingDependency(expr string, valueJson map[string]interface{}) bool {
	matches := ppRefPattern.FindAllStringSubmatch(expr, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		ref := m[1]
		if dot := strings.IndexByte(ref, '.'); dot != -1 {
			ref = ref[:dot]
		}
		if _, ok := valueJson[ref]; !ok {
			return true
		}
	}
	return false
}

// collectMissingPpRefs returns the deduplicated list of "_pp*" top-level
// keys referenced by expr that are NOT present in valueJson. Used only on
// the fixpoint failure path to build a precise diagnostic.
func collectMissingPpRefs(expr string, valueJson map[string]interface{}) []string {
	matches := ppRefPattern.FindAllStringSubmatch(expr, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		ref := m[1]
		if dot := strings.IndexByte(ref, '.'); dot != -1 {
			ref = ref[:dot]
		}
		if _, ok := valueJson[ref]; ok {
			continue
		}
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}

// NEW FUNCTION - ARRAY EXPANSION WITH OPTIMIZATIONS
// ctx is optional; when present and containing a request logger, that logger is used for correlation_id etc.
func (updater *DynamicJsonUpdater) InterpolateValuesWithArrayExpansion(ctx context.Context, sfMapping []types.SFRequestDB, valueJson map[string]interface{}) ([]types.SFRequest, error) {
	log := getRequestLogger(ctx, updater.log)
	if ctx != nil {
		ctx = SetRequestLogger(ctx, log)
	}
	start := time.Now()
	log.Info("InterpolateValuesWithArrayExpansion started - processing templates: ", len(sfMapping))

	// Single pass: fast-path conversion while checking if any template is array-based.
	hasArrays := false
	regularTemplates := make([]types.SFRequest, 0, len(sfMapping))
	for _, template := range sfMapping {
		if template.ArrayPath != "" {
			hasArrays = true
			break
		}
		regularTemplates = append(regularTemplates, types.SFRequest{
			URL:    template.URL,
			Method: template.Method,
			RefID:  template.RefID,
			Body:   template.Body, // Reference, not copy
		})
	}

	// Fast path: no arrays found
	if !hasArrays {
		log.Info("Fast path: No arrays detected, using regular processing")
		result, err := updater.InterpolateValues(ctx, regularTemplates, valueJson)
		if err != nil {
			log.Errorw("Fast path processing failed", "error", err)
			// return nil, err
		}
		duration := time.Since(start)
		log.Info("Fast path completed in: ", duration, " with results: ", len(result))
		return result, nil
	}

	log.Info("Slow path: Arrays detected, using full array processing")
	// Slow path: Arrays exist - need full processing
	return updater.processWithArrays(ctx, sfMapping, valueJson, start)
}

// Separate function for array processing to keep main function clean
func (updater *DynamicJsonUpdater) processWithArrays(ctx context.Context, sfMapping []SFRequestDB, valueJson map[string]interface{}, start time.Time) ([]SFRequest, error) {
	log := getRequestLogger(ctx, updater.log)
	log.Info("processWithArrays started - analyzing template composition")

	n := len(sfMapping)
	arrayTemplates := make([]SFRequest, 0, n)
	regularTemplates := make([]SFRequest, 0, n)
	arrayPathMap := make(map[string]string, n)
	lookupConfigMap := make(map[string]*LookupConfig, n)
	filterConditionsMap := make(map[string]*types.FilterConditions, n)
	totalArrayElements := 0

	var keyBuilder strings.Builder
	for _, template := range sfMapping {
		sfRequest := SFRequest{
			URL:    template.URL,
			Method: template.Method,
			RefID:  template.RefID,
			Body:   template.Body,
		}

		if template.ArrayPath != "" {
			// Check if array path is valid before treating as array template
			arrayData := getByDotPath(valueJson, "(("+template.ArrayPath+"))")
			isValidArray := false

			// Debug: Log array path lookup
			log.Info(fmt.Sprintf("Array expansion debug - RefID=%s, ArrayPath='%s', Found=%v, DataType=%T",
				template.RefID, template.ArrayPath, arrayData != nil, arrayData))

			if arrayData != nil {
				if arr, ok := arrayData.([]interface{}); ok && len(arr) > 0 {
					// Valid non-empty array - treat as array template
					arraySize := len(arr)
					totalArrayElements += arraySize
					log.Info("Array template: ", template.ArrayPath, " has elements: ", arraySize)
					isValidArray = true
				} else if arr, ok := arrayData.([]interface{}); ok && len(arr) == 0 {
					// Empty array - treat as array template (will generate 0 requests)
					log.Info("Array template: ", template.ArrayPath, " has 0 elements")
					isValidArray = true
				} else {
					log.Warnw("ArrayPath does not point to an array, falling back to regular template", "arrayPath", template.ArrayPath, "dataType", fmt.Sprintf("%T", arrayData))
				}
			} else {
				log.Warnw("ArrayPath not found in valueJson, falling back to regular template", "arrayPath", template.ArrayPath)
			}

			if isValidArray {
				// Treat as array template
				arrayTemplates = append(arrayTemplates, sfRequest)

				// Fixed: Use reference ID to make key unique
				keyBuilder.Reset()
				keyBuilder.Grow(len(template.URL) + len(template.Method) + len(template.RefID) + 2)
				keyBuilder.WriteString(template.URL)
				keyBuilder.WriteByte('|')
				keyBuilder.WriteString(template.Method)
				keyBuilder.WriteByte('|')
				keyBuilder.WriteString(template.RefID)
				arrayPathMap[keyBuilder.String()] = template.ArrayPath
				lookupConfigMap[keyBuilder.String()] = template.Lookup
				filterConditionsMap[keyBuilder.String()] = template.FilterConditions
			} else {
				// Fallback to regular template processing
				log.Info("Converting array template to regular template due to invalid/missing array path: ", template.ArrayPath)
				regularTemplates = append(regularTemplates, sfRequest)
			}
		} else {
			regularTemplates = append(regularTemplates, sfRequest)
		}
	}

	// Update final counts after smart fallback processing
	finalArrayCount := len(arrayTemplates)
	finalRegularCount := len(regularTemplates)
	log.Info("Template separation completed - final array templates: ", finalArrayCount, " final regular templates: ", finalRegularCount, " total array elements estimated: ", totalArrayElements)

	// Process regular templates using existing optimized method
	var regularResults []SFRequest
	var err error
	if len(regularTemplates) > 0 {
		log.Info("Processing regular templates: ", len(regularTemplates))
		regularResults, err = updater.InterpolateValues(ctx, regularTemplates, valueJson)
		if err != nil {
			log.Errorw("Regular template processing failed", "error", err)
			return nil, err
		}
		log.Info("Regular template processing completed - results: ", len(regularResults))
	}

	// Process array templates with streaming approach
	var arrayResults []SFRequest
	if len(arrayTemplates) > 0 {
		log.Info("Processing array templates: ", len(arrayTemplates), " with total elements: ", totalArrayElements)
		arrayParamsCache := newRequestParameterCache()
		arrayResults, err = updater.processArrayTemplatesStreamingWithArrayPath(ctx, arrayPathMap, lookupConfigMap, filterConditionsMap, arrayTemplates, valueJson, totalArrayElements, arrayParamsCache)
		if err != nil {
			log.Errorw("Array template processing failed", "error", err)
			return nil, err
		}
		log.Info("Array template processing completed - results: ", len(arrayResults))
	}

	// Combine results with pre-allocated capacity
	allResults := make([]SFRequest, 0, len(regularResults)+len(arrayResults))
	allResults = append(allResults, regularResults...)
	allResults = append(allResults, arrayResults...)

	duration := time.Since(start)
	log.Info("Array expansion completed in: ", duration, " for regular mappings: ", len(regularTemplates), " array mappings: ", len(arrayTemplates), " total requests: ", len(allResults))
	// Avoid logging full expanded payloads; they are extremely large in production paths.
	return allResults, nil
}

// Optimized array processing with better key handling
func (updater *DynamicJsonUpdater) processArrayTemplatesStreamingWithArrayPath(ctx context.Context, arrayPathMap map[string]string, lookupConfigMap map[string]*LookupConfig, filterConditionsMap map[string]*types.FilterConditions, arrayTemplates []SFRequest, valueJson map[string]interface{}, totalArrayElements int, paramsCache *requestParameterCache) ([]SFRequest, error) {
	log := getRequestLogger(ctx, updater.log)
	if len(arrayTemplates) == 0 {
		log.Info("No array templates to process, returning empty result")
		return nil, nil
	}

	processStart := time.Now()
	log.Info("processArrayTemplatesStreaming started - templates: ", len(arrayTemplates), " total elements: ", totalArrayElements)

	// Determine if memory pooling should be used
	arrCfg := updater.getArrayConfig()
	useMemoryPool := arrCfg.EnableMemoryPool && totalArrayElements > arrCfg.MemoryPoolThreshold

	if useMemoryPool {
		log.Info("Memory pooling ENABLED - element count: ", totalArrayElements, " exceeds threshold: ", arrCfg.MemoryPoolThreshold)
	} else {
		log.Info("Memory pooling DISABLED - element count: ", totalArrayElements, " below threshold: ", arrCfg.MemoryPoolThreshold)
	}

	var results []SFRequest
	var mu sync.Mutex
	var wg sync.WaitGroup
	errorBufferSize := len(arrayTemplates) * 10
	totalWorkEstimate := totalArrayElements + len(arrayTemplates)
	if totalWorkEstimate > errorBufferSize {
		errorBufferSize = totalWorkEstimate
	}
	if errorBufferSize < 1 {
		errorBufferSize = 1
	}
	errors := make(chan error, errorBufferSize)
	sendErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case errors <- err:
		default:
			log.Warnw("Dropping array processing error due to full error channel", "error", err)
		}
	}

	maxPar := updater.MaxParallelGoroutines
	if maxPar < 1 {
		maxPar = 0
	}
	var outerSem chan struct{}

	outerCap := commoninit.GetConfigInt(constants.ServerMaxParallelDynamicJsonTemplateGoroutines, constants.DefaultMaxParallelTemplateGoroutines)
	if outerCap > 0 {
		if maxPar > 0 && outerCap > maxPar {
			outerCap = maxPar
		}
		outerSem = make(chan struct{}, outerCap)
	}
	globalSem := getGlobalWorkerSemaphore()

	// Pre-allocate key builder for each goroutine to avoid allocations
	keyBuilderPool := sync.Pool{
		New: func() interface{} {
			return &strings.Builder{}
		},
	}

	// Process each array template concurrently
	for templateIndex, template := range arrayTemplates {
		if outerSem != nil {
			outerSem <- struct{}{}
		}
		wg.Add(1)
		go func(templateIndex int, template SFRequest) {
			defer wg.Done()
			if outerSem != nil {
				defer func() { <-outerSem }()
			}
			templateStart := time.Now()
			defer func() {
				if r := recover(); r != nil {
					panicErr := fmt.Errorf("panic in array template goroutine (templateIndex=%d): %v", templateIndex, r)
					log.Errorw("Recovered panic while processing array template",
						"templateIndex", templateIndex,
						"panic", r,
						"stackTrace", string(debug.Stack()))
					sendErr(panicErr)
				}
			}()
			// Get ArrayPath from lookup map with optimized key generation
			keyBuilder := keyBuilderPool.Get().(*strings.Builder)
			defer func() {
				keyBuilder.Reset()
				keyBuilderPool.Put(keyBuilder)
			}()

			keyBuilder.Grow(len(template.URL) + len(template.Method) + len(template.RefID) + 2)
			keyBuilder.WriteString(template.URL)
			keyBuilder.WriteByte('|')
			keyBuilder.WriteString(template.Method)
			keyBuilder.WriteByte('|')
			keyBuilder.WriteString(template.RefID)

			key := keyBuilder.String()
			arrayPath, exists := arrayPathMap[key]
			if !exists {
				log.Errorw("ArrayPath not found for template", "templateIndex", templateIndex, "URL", template.URL, "Method", template.Method)
				return
			}
			templateLookup := lookupConfigMap[key]
			filterConditions := filterConditionsMap[key]

			// Enhanced array data validation with fallback options
			validateStart := time.Now()
			arr, err := updater.validateAndProcessArrayData(arrayPath, valueJson, template, templateIndex, log)
			if err != nil {
				if updater.getArrayConfig().SkipInvalidArrayPaths {
					log.Warnw("Skipping template due to invalid array path", "error", err, "templateIndex", templateIndex)
					return // Skip this template gracefully
				} else {
					log.Errorw("Fatal error processing array template", "error", err, "templateIndex", templateIndex)
					sendErr(fmt.Errorf("array processing error: %v", err))
					return
				}
			}
			arrayValidationMs := time.Since(validateStart).Milliseconds()

			if len(arr) == 0 {
				log.Info("Empty array at path: ", arrayPath, " - no requests will be generated")
				return // No elements to process
			}

			lookupPrepStart := time.Now()
			preparedLookup := prepareTemplateLookup(templateLookup, valueJson, log)
			lookupPrepMs := time.Since(lookupPrepStart).Milliseconds()

			arraySize := len(arr)
			log.Info("Processing array template: ", templateIndex, " elements: ", arraySize, " from path: ", arrayPath)
			baseParameters := getRequestScopedParameters(paramsCache, valueJson)
			collectUnresolvedDetails := arrCfg.CollectUnresolvedDetails

			// Process array elements with streaming approach
			var elementWg sync.WaitGroup
			elementResults := make([]SFRequest, arraySize)
			elementKept := make([]bool, arraySize)

			// Thread-safe collection for unresolved fields across all elements
			// Pre-allocate with estimated capacity based on array size to reduce reallocations
			var unresolvedMu sync.Mutex
			allUnresolvedExpressions := make([]string, 0, arraySize*2)        // Estimate ~2 unresolved per element
			allUnresolvedFailedExpressions := make([]string, 0, arraySize/10) // Fewer failures expected (~10%)
			allUnresolvedDotPaths := make([]string, 0, arraySize*2)           // Estimate ~2 unresolved per element
			allUnresolvedSFVariables := make([]string, 0, arraySize/10)       // Fewer SF variable issues expected
			unresolvedExpressionCount := 0
			unresolvedFailedExpressionCount := 0
			unresolvedDotPathCount := 0
			unresolvedSFVariableCount := 0

			workerCount := determineArrayWorkerCount(arraySize, maxPar)
			jobs := make(chan int, workerCount)
			elementProcessingStart := time.Now()
			for workerIdx := 0; workerIdx < workerCount; workerIdx++ {
				elementWg.Add(1)
				go func() {
					defer elementWg.Done()
					if globalSem != nil {
						globalSem <- struct{}{}
						defer func() { <-globalSem }()
					}
					defer func() {
						if r := recover(); r != nil {
							panicErr := fmt.Errorf("panic in array element worker (templateIndex=%d): %v", templateIndex, r)
							log.Errorw("Recovered panic while processing array worker",
								"templateIndex", templateIndex,
								"panic", r,
								"stackTrace", string(debug.Stack()))
							sendErr(panicErr)
						}
					}()

					for i := range jobs {
						element := arr[i]
						processedRequest, unresolved, skipped, err := updater.processArrayElement(template, element, i, valueJson, arrayPath, preparedLookup, filterConditions, useMemoryPool, collectUnresolvedDetails, log, baseParameters)
						if err != nil {
							log.Errorw("Failed to process array element", "elementIndex", i, "templateIndex", templateIndex, "error", err)
							sendErr(fmt.Errorf("error processing array element %d: %v", i, err))
							continue
						}
						if skipped {
							continue
						}

						elementResults[i] = processedRequest
						elementKept[i] = true

						if unresolved != nil {
							unresolvedMu.Lock()
							unresolvedExpressionCount += unresolved.ExpressionCount
							unresolvedFailedExpressionCount += unresolved.FailedExpressionCount
							unresolvedDotPathCount += unresolved.DotPathCount
							unresolvedSFVariableCount += unresolved.SFVariableCount
							if collectUnresolvedDetails {
								allUnresolvedExpressions = append(allUnresolvedExpressions, unresolved.Expressions...)
								allUnresolvedFailedExpressions = append(allUnresolvedFailedExpressions, unresolved.FailedExpressions...)
								allUnresolvedDotPaths = append(allUnresolvedDotPaths, unresolved.DotPaths...)
								allUnresolvedSFVariables = append(allUnresolvedSFVariables, unresolved.SFVariables...)
							}
							unresolvedMu.Unlock()
						}
					}
				}()
			}
			for i := range arr {
				jobs <- i
			}
			close(jobs)

			elementWg.Wait()
			elementProcessingMs := time.Since(elementProcessingStart).Milliseconds()

			// Log aggregated unresolved fields for the entire array (after all elements are processed)
			unresolvedLogStart := time.Now()
			if collectUnresolvedDetails && len(allUnresolvedExpressions) > 0 {
				log.Info("Unresolved expressions (missing parameters) for template: ", templateIndex, " arrayPath: ", arrayPath, " count: ", len(allUnresolvedExpressions), " fields: ", allUnresolvedExpressions)
			} else if !collectUnresolvedDetails && unresolvedExpressionCount > 0 {
				log.Info("Unresolved expressions (missing parameters) for template: ", templateIndex, " arrayPath: ", arrayPath, " count: ", unresolvedExpressionCount)
			}

			if collectUnresolvedDetails && len(allUnresolvedFailedExpressions) > 0 {
				log.Info("Failed expression evaluations for template: ", templateIndex, " arrayPath: ", arrayPath, " count: ", len(allUnresolvedFailedExpressions), " fields: ", allUnresolvedFailedExpressions)
			} else if !collectUnresolvedDetails && unresolvedFailedExpressionCount > 0 {
				log.Info("Failed expression evaluations for template: ", templateIndex, " arrayPath: ", arrayPath, " count: ", unresolvedFailedExpressionCount)
			}

			if collectUnresolvedDetails && len(allUnresolvedDotPaths) > 0 {
				log.Info("Unresolved dot paths for template: ", templateIndex, " arrayPath: ", arrayPath, " count: ", len(allUnresolvedDotPaths), " fields: ", allUnresolvedDotPaths)
			} else if !collectUnresolvedDetails && unresolvedDotPathCount > 0 {
				log.Info("Unresolved dot paths for template: ", templateIndex, " arrayPath: ", arrayPath, " count: ", unresolvedDotPathCount)
			}

			if collectUnresolvedDetails && len(allUnresolvedSFVariables) > 0 {
				log.Info("Unresolved SF variables for template: ", templateIndex, " arrayPath: ", arrayPath, " count: ", len(allUnresolvedSFVariables), " fields: ", allUnresolvedSFVariables)
			} else if !collectUnresolvedDetails && unresolvedSFVariableCount > 0 {
				log.Info("Unresolved SF variables for template: ", templateIndex, " arrayPath: ", arrayPath, " count: ", unresolvedSFVariableCount)
			}
			unresolvedLogMs := time.Since(unresolvedLogStart).Milliseconds()

			// Add results to main result set
			generatedCount := 0
			mergeStart := time.Now()
			mu.Lock()
			for i, keep := range elementKept {
				if keep {
					results = append(results, elementResults[i])
					generatedCount++
				}
			}
			mu.Unlock()
			mergeMs := time.Since(mergeStart).Milliseconds()

			log.Info("Completed array template: ", templateIndex, " generated requests: ", generatedCount, " from elements: ", arraySize)
			log.Infow("Array template microtimers",
				"templateIndex", templateIndex,
				"arrayPath", arrayPath,
				"elementCount", arraySize,
				"workerCount", workerCount,
				"validateArrayMs", arrayValidationMs,
				"prepareLookupMs", lookupPrepMs,
				"elementProcessingMs", elementProcessingMs,
				"unresolvedLogMs", unresolvedLogMs,
				"mergeResultsMs", mergeMs,
				"templateTotalMs", time.Since(templateStart).Milliseconds())

		}(templateIndex, template)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		if err != nil {
			log.Errorw("Array processing error", "error", err)
			return nil, err
		}
	}

	log.Infow("processArrayTemplatesStreaming completed",
		"totalResultsGenerated", len(results),
		"templates", len(arrayTemplates),
		"totalElements", totalArrayElements,
		"durationMs", time.Since(processStart).Milliseconds())
	return results, nil
}

func determineArrayWorkerCount(arraySize int, maxPar int) int {
	if arraySize <= 0 {
		return 1
	}

	workers := arraySize
	if maxPar > 0 && workers > maxPar {
		workers = maxPar
	}

	if maxPar <= 0 {
		defaultWorkers := runtime.GOMAXPROCS(0) * 2
		if defaultWorkers < 1 {
			defaultWorkers = 1
		}
		if workers > defaultWorkers {
			workers = defaultWorkers
		}
	}

	if workers < 1 {
		return 1
	}
	return workers
}

// Enhanced array data validation with fallback options
func (updater *DynamicJsonUpdater) validateAndProcessArrayData(arrayPath string, valueJson map[string]interface{}, _ SFRequest, templateIndex int, log contracts.Logger) ([]interface{}, error) {
	if log == nil {
		log = updater.log
	}
	// Get array data
	arrayData := getByDotPath(valueJson, "(("+arrayPath+"))")

	arrCfgLocal := updater.getArrayConfig()

	if arrayData == nil {
		log.Errorw("Array data not found for path", "arrayPath", arrayPath, "templateIndex", templateIndex)

		if arrCfgLocal.FallbackToSingleElement {
			log.Info("Falling back to single element for missing path: ", arrayPath)
			return []interface{}{valueJson}, nil
		}

		return nil, fmt.Errorf("array data not found for path: %s", arrayPath)
	}

	// Check if it's actually an array
	if arr, ok := arrayData.([]interface{}); ok {
		log.Info("Valid array found at path: ", arrayPath, " with elements: ", len(arr))
		return arr, nil
	}

	// Not an array - handle different data types
	log.Warnw("Data at path is not an array", "arrayPath", arrayPath, "dataType", fmt.Sprintf("%T", arrayData), "templateIndex", templateIndex)

	switch actualData := arrayData.(type) {
	case map[string]interface{}:
		if arrCfgLocal.FallbackToSingleElement {
			log.Info("Treating single object as single-element array for path: ", arrayPath)
			return []interface{}{actualData}, nil
		}

	case string, int, float64, bool:
		if arrCfgLocal.FallbackToSingleElement {
			log.Info("Treating primitive value as single-element array for path: ", arrayPath)
			return []interface{}{actualData}, nil
		}

	default:
		log.Errorw("Unknown data type at array path", "arrayPath", arrayPath, "dataType", fmt.Sprintf("%T", arrayData))
	}

	return nil, fmt.Errorf("data at path %s is not an array (type: %T)", arrayPath, arrayData)
}

// resolveFilterExpressionPaths replaces dotted path references (e.g. current.authStatus) in the
// expression with their resolved literal values so govaluate only sees literals and operators.
// This avoids govaluate parsing identifiers containing '_' (from dot→underscore), which causes
// "Invalid token: '_'" when evaluating selectWhen/skipWhen conditions.
func resolveFilterExpressionPathsWithParameters(expr string, baseParameters map[string]interface{}, overlayParameters map[string]interface{}) string {
	var out strings.Builder
	out.Grow(len(expr) * 2) // may grow due to literal formatting

	inQuotes := false
	quoteChar := byte(0)
	i := 0

	for i < len(expr) {
		ch := expr[i]

		// Track quoted regions so we don't replace inside strings
		if (ch == '"' || ch == '\'') && (i == 0 || expr[i-1] != '\\') {
			if !inQuotes {
				inQuotes = true
				quoteChar = ch
			} else if ch == quoteChar {
				inQuotes = false
				quoteChar = 0
			}
			out.WriteByte(ch)
			i++
			continue
		}

		if inQuotes {
			out.WriteByte(ch)
			i++
			continue
		}

		// Outside quotes: check for start of path (identifier: letter or underscore)
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_' {
			start := i
			i++
			for i < len(expr) {
				c := expr[i]
				if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' {
					i++
				} else {
					break
				}
			}
			path := strings.TrimSpace(expr[start:i])
			if path != "" {
				key := convertToUnderscoreKey(path)
				if val, ok := getLayeredParameterValue(key, baseParameters, overlayParameters); ok && val != nil {
					out.WriteString(formatValueAsGovaluateLiteral(val))
				} else {
					// Missing or nil: emit empty string literal so expression remains valid
					out.WriteString("''")
				}
			} else {
				out.WriteString(expr[start:i])
			}
			continue
		}

		out.WriteByte(ch)
		i++
	}

	return out.String()
}

func getLayeredParameterValue(key string, baseParameters map[string]interface{}, overlayParameters map[string]interface{}) (interface{}, bool) {
	if overlayParameters != nil {
		if val, ok := overlayParameters[key]; ok {
			return val, true
		}
	}
	if baseParameters != nil {
		if val, ok := baseParameters[key]; ok {
			return val, true
		}
	}
	return nil, false
}

func resolveFilterExpressionPaths(expr string, valueJson map[string]interface{}, paramsCache *requestParameterCache) string {
	parameters := getRequestScopedParameters(paramsCache, valueJson)
	return resolveFilterExpressionPathsWithParameters(expr, parameters, nil)
}

// formatValueAsGovaluateLiteral formats a value as a govaluate expression literal.
func formatValueAsGovaluateLiteral(val interface{}) string {
	switch v := val.(type) {
	case nil:
		return "''"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		// Govaluate uses single-quoted strings; escape ' by doubling
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	default:
		// Non-scalar (map/slice) cannot be a govaluate literal; use empty string
		return "''"
	}
}

func (updater *DynamicJsonUpdater) evaluateFilterExpression(expr string, valueJson map[string]interface{}, log contracts.Logger, paramsCache *requestParameterCache) (bool, error) {
	if expr == "" {
		return false, nil
	}

	cleanExpr := strings.TrimPrefix(strings.TrimSuffix(expr, "}}"), "{{")
	cleanExpr = strings.TrimSpace(cleanExpr)
	// Resolve dotted paths to literals before parsing so govaluate never sees identifiers with '_'
	resolvedExpr := resolveFilterExpressionPaths(cleanExpr, valueJson, paramsCache)

	// Resolved expression is literal-only (e.g. 'Active' == 'Active'); do not run smartReplaceSpecialChars
	// or spaces become underscores and govaluate fails with "Invalid token: '_==_'"
	res, err := evaluateExpression(resolvedExpr, valueJson, true, log, paramsCache)
	if err != nil {
		if _, isMissingParam := err.(*MissingParameterError); isMissingParam {
			return false, nil
		}
		return false, err
	}
	if res == nil {
		return false, nil
	}
	boolVal, ok := res.(bool)
	if !ok {
		return false, fmt.Errorf("filter expression did not evaluate to bool: %s", expr)
	}
	return boolVal, nil
}

func (updater *DynamicJsonUpdater) evaluateFilterExpressionWithParameters(expr string, baseParameters map[string]interface{}, overlayParameters map[string]interface{}, log contracts.Logger) (bool, error) {
	if expr == "" {
		return false, nil
	}

	cleanExpr := strings.TrimPrefix(strings.TrimSuffix(expr, "}}"), "{{")
	cleanExpr = strings.TrimSpace(cleanExpr)

	resolvedExpr := resolveFilterExpressionPathsWithParameters(cleanExpr, baseParameters, overlayParameters)
	res, err := evaluateExpressionWithParameters(resolvedExpr, baseParameters, overlayParameters, true, log)
	if err != nil {
		if _, isMissingParam := err.(*MissingParameterError); isMissingParam {
			return false, nil
		}
		return false, err
	}
	if res == nil {
		return false, nil
	}
	boolVal, ok := res.(bool)
	if !ok {
		return false, fmt.Errorf("filter expression did not evaluate to bool: %s", expr)
	}
	return boolVal, nil
}

func (updater *DynamicJsonUpdater) shouldSkipArrayElement(filter *types.FilterConditions, valueJson map[string]interface{}, log contracts.Logger, paramsCache *requestParameterCache) (bool, error) {
	if filter == nil {
		return false, nil
	}
	if filter.SkipWhen != "" {
		shouldSkip, err := updater.evaluateFilterExpression(filter.SkipWhen, valueJson, log, paramsCache)
		if err != nil {
			return false, err
		}
		if shouldSkip {
			return true, nil
		}
	}
	if filter.SelectWhen != "" {
		shouldSelect, err := updater.evaluateFilterExpression(filter.SelectWhen, valueJson, log, paramsCache)
		if err != nil {
			return false, err
		}
		if !shouldSelect {
			return true, nil
		}
	}
	return false, nil
}

func (updater *DynamicJsonUpdater) shouldSkipArrayElementWithParameters(filter *types.FilterConditions, baseParameters map[string]interface{}, overlayParameters map[string]interface{}, log contracts.Logger) (bool, error) {
	if filter == nil {
		return false, nil
	}
	if filter.SkipWhen != "" {
		shouldSkip, err := updater.evaluateFilterExpressionWithParameters(filter.SkipWhen, baseParameters, overlayParameters, log)
		if err != nil {
			return false, err
		}
		if shouldSkip {
			return true, nil
		}
	}
	if filter.SelectWhen != "" {
		shouldSelect, err := updater.evaluateFilterExpressionWithParameters(filter.SelectWhen, baseParameters, overlayParameters, log)
		if err != nil {
			return false, err
		}
		if !shouldSelect {
			return true, nil
		}
	}
	return false, nil
}

// Process single array element with optional memory pooling and array path transformation
func (updater *DynamicJsonUpdater) processArrayElement(template SFRequest, element interface{}, index int, valueJson map[string]interface{}, arrayPath string, lookup *preparedLookup, filter *types.FilterConditions, useMemoryPool bool, collectUnresolvedDetails bool, log contracts.Logger, baseParameters map[string]interface{}) (SFRequest, *UnresolvedFields, bool, error) {
	if log == nil {
		log = updater.log
	}
	var newRequest SFRequest
	var newBody map[string]interface{}

	// Lightweight overlay context to avoid full map copy per array element.
	expandedValueJson := make(map[string]interface{}, 4)
	expandedValueJson["__root"] = valueJson
	expandedValueJson["current"] = element
	expandedValueJson["index"] = index
	var lookupAlias string
	var lookupMatched interface{}
	if lookup != nil {
		currentVal := extractLookupPathValue(element, lookup.CurrentPath)
		joinKey := normalizeLookupJoinKey(currentVal)
		if joinKey != "" {
			if matched, exists := lookup.Index[joinKey]; exists && matched != nil {
				expandedValueJson[lookup.Alias] = matched
				lookupAlias = lookup.Alias
				lookupMatched = matched
			}
		}
	}

	overlayScope := map[string]interface{}{
		"current": element,
		"index":   index,
	}
	if lookupAlias != "" && lookupMatched != nil {
		overlayScope[lookupAlias] = lookupMatched
	}
	overlayParameters := make(map[string]interface{}, 32)
	extractAllKeysEfficient(overlayScope, "", overlayParameters)

	// Apply array filter conditions (skip first, then select)
	shouldSkip, err := updater.shouldSkipArrayElementWithParameters(filter, baseParameters, overlayParameters, log)
	if err != nil {
		return SFRequest{}, nil, false, err
	}
	if shouldSkip {
		return SFRequest{}, nil, true, nil
	}

	if useMemoryPool {
		// Use memory pools for high-volume scenarios
		pooledRequest := requestPool.Get().(SFRequest)
		defer requestPool.Put(pooledRequest)

		pooledBody := bodyPool.Get().(map[string]interface{})
		defer func() {
			for k := range pooledBody {
				delete(pooledBody, k)
			}
			bodyPool.Put(pooledBody)
		}()

		newBody = pooledBody
	} else {
		newBody = make(map[string]interface{}, len(template.Body))
	}

	// Process template body with array path transformation
	fieldCount := 0
	unresolved := &UnresolvedFields{}
	if collectUnresolvedDetails {
		// Allocate only when detailed unresolved diagnostics are enabled.
		unresolved.Expressions = make([]string, 0, 10)
		unresolved.FailedExpressions = make([]string, 0, 5)
		unresolved.DotPaths = make([]string, 0, 10)
		unresolved.SFVariables = make([]string, 0, 5)
	}

	for key, rawVal := range template.Body {
		valStr, ok := rawVal.(string)
		if !ok {
			if rawVal != nil {
				newBody[key] = rawVal
				fieldCount++
			}
			continue
		}

		if isExpression(valStr) {
			res, err := evaluateExpressionWithParameters(valStr, baseParameters, overlayParameters, false, log)
			if err != nil {
				if _, isMissingParam := err.(*MissingParameterError); isMissingParam {
					unresolved.ExpressionCount++
					if collectUnresolvedDetails {
						unresolved.Expressions = append(unresolved.Expressions, formatUnresolvedField(index, key, valStr))
					}
					continue
				}
				// Collect failed expression evaluation errors for batch logging
				unresolved.FailedExpressionCount++
				if collectUnresolvedDetails {
					unresolved.FailedExpressions = append(unresolved.FailedExpressions, formatFailedExpression(index, key, valStr, err))
				}
				continue
			}
			// Include the key only for non-null, non-blank values; omit null and blank so SF does not receive them.
			if shouldIncludeInBody(res) {
				newBody[key] = res
				fieldCount++
			}
		} else if isDotPath(valStr) {
			// Transform dot path for array element processing
			transformedPath := updater.transformDotPathForArrayElement(valStr, arrayPath)
			// updater.log.Info("Transformed dot path: ", valStr, " -> ", transformedPath, " for array element: ", index, " key: ", key)

			val := getByDotPath(expandedValueJson, transformedPath)
			if shouldIncludeInBody(val) {
				newBody[key] = val
				fieldCount++
			} else if val == nil {
				unresolved.DotPathCount++
				if collectUnresolvedDetails {
					unresolved.DotPaths = append(unresolved.DotPaths, formatUnresolvedField(index, key, transformedPath))
				}
			}
		} else if isSFVariable(valStr) {
			// Handle <Variable> syntax in body fields for array elements; omit blank like InterpolateValues.
			val := substituteSFVariables(valStr, expandedValueJson)
			if val != valStr && val != "" {
				newBody[key] = val
				fieldCount++
			} else if val == valStr {
				unresolved.SFVariableCount++
				if collectUnresolvedDetails {
					unresolved.SFVariables = append(unresolved.SFVariables, formatUnresolvedField(index, key, valStr))
				}
			}
		} else {
			if valStr != "" {
				newBody[key] = valStr
				fieldCount++
			}
		}
	}

	// Create final request with exactly 4 parameters
	// Transform reference ID to include index
	refID := template.RefID
	if strings.Contains(refID, "{{index}}") {
		refID = strings.ReplaceAll(refID, "{{index}}", strconv.Itoa(index))
	} else {
		// Append index to reference ID
		refID = fmt.Sprintf("%s_%d", refID, index)
	}

	if useMemoryPool {
		newRequest = SFRequest{
			URL:    template.URL,
			Method: template.Method,
			RefID:  refID,
			Body:   make(map[string]interface{}, len(newBody)),
		}
		// Copy body only when pooled map must be detached before put-back.
		for k, v := range newBody {
			newRequest.Body[k] = v
		}
	} else {
		newRequest = SFRequest{
			URL:    template.URL,
			Method: template.Method,
			RefID:  refID,
			Body:   newBody,
		}
	}

	// Process URL variables
	newRequest.URL = substituteSFVariables(newRequest.URL, expandedValueJson)

	// Log successful processing (only log every 10th element to avoid spam)
	if index%10 == 0 || index < 5 {
		log.Debugw("Processed array element", "index", index, "fieldsPopulated", fieldCount, "refID", refID)
	}

	return newRequest, unresolved, false, nil
}

// Transform dot path for array element processing
// If the dot path starts with the array path, replace it with "current"
func (updater *DynamicJsonUpdater) transformDotPathForArrayElement(dotPath string, arrayPath string) string {
	// Strip the circular brackets before processing
	cleanPath := strings.TrimPrefix(strings.TrimSuffix(dotPath, "))"), "((")

	// If the path starts with the array path, replace it with "current"
	if arrayPath != "" && strings.HasPrefix(cleanPath, arrayPath) {
		// Replace the array path prefix with "current"
		remaining := strings.TrimPrefix(cleanPath, arrayPath)

		// If there's a remaining path after the array path, add it to current
		if strings.HasPrefix(remaining, ".") {
			return "((" + "current" + remaining + "))"
		} else if remaining == "" {
			return "((current))"
		}
	}

	// If no transformation is needed, return the original path
	return dotPath
}

// Update configuration from config file
func (updater *DynamicJsonUpdater) UpdateArrayConfig(config *ArrayProcessingConfig) {
	updater.log.Info("Updating array config - MemoryPoolThreshold: ", config.MemoryPoolThreshold, " EnableMemoryPool: ", config.EnableMemoryPool, " FallbackToSingleElement: ", config.FallbackToSingleElement, " SkipInvalidArrayPaths: ", config.SkipInvalidArrayPaths, " CollectUnresolvedDetails: ", config.CollectUnresolvedDetails)
	updater.setArrayConfig(config)
}

// ReloadConfigFromFile reloads array processing configuration from config file
func (updater *DynamicJsonUpdater) ReloadConfigFromFile() {
	newConfig := loadArrayConfigFromFile()
	updater.UpdateArrayConfig(newConfig)
	updater.log.Info("Array processing configuration reloaded from config file")
}

// Detect if string is expression using curly braces
func isExpression(s string) bool {
	return strings.HasPrefix(s, "{{") && strings.HasSuffix(s, "}}")
}

// Detect if string is dot path using circular brackets
func isDotPath(s string) bool {
	return strings.HasPrefix(s, "((") && strings.HasSuffix(s, "))")
}

// Detect if string uses <Variable> syntax.
// Uses the compiled sfVarRegex (<...>) so comparison operators like
// "x > 5 && x < 10" are not misidentified as SF variable references.
func isSFVariable(s string) bool {
	return sfVarRegex.MatchString(s)
}

// Dot-path lookup with array support: NameMatchDs.Score[0].value
func getByDotPath(data interface{}, path string) interface{} {
	// Strip the circular brackets before evaluation
	cleanPath := strings.TrimPrefix(strings.TrimSuffix(path, "))"), "((")
	segments := strings.Split(cleanPath, ".")
	curr := data

	for _, segment := range segments {
		// Handle array access
		bracketIndex := strings.Index(segment, "[")
		if bracketIndex != -1 && strings.HasSuffix(segment, "]") {
			key := segment[:bracketIndex]
			indexStr := segment[bracketIndex+1 : len(segment)-1]

			currMap, ok := curr.(map[string]interface{})
			if !ok {
				return nil
			}
			arr, ok := currMap[key].([]interface{})
			if !ok {
				return nil
			}

			index, err := strconv.Atoi(indexStr)
			if err != nil || index < 0 || index >= len(arr) {
				return nil
			}
			curr = arr[index]
		} else {
			m, ok := curr.(map[string]interface{})
			if !ok {
				return nil
			}
			if next, exists := m[segment]; exists {
				curr = next
				continue
			}
			if root, ok := m["__root"].(map[string]interface{}); ok {
				curr = root[segment]
				continue
			}
			return nil
		}
	}

	return curr
}

// Substitute <Variable> in URLs
func substituteSFVariables(url string, valueJson map[string]interface{}) string {
	return sfVarRegex.ReplaceAllStringFunc(url, func(match string) string {
		fieldPath := strings.Trim(match, "<>")
		val := getByDotPath(valueJson, fieldPath)
		if val == nil {
			return match
		}
		return fmt.Sprintf("%v", val)
	})
}

var sfVarRegex = regexp.MustCompile(`<([^>]+)>`)
