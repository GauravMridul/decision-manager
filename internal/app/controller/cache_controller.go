package controller

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"decision-manager/internal/app/constants"
	commoninit "decision-manager/internal/app/init"
	"decision-manager/internal/app/service/cache_service"

	"github.com/gin-gonic/gin"
)

type CacheController struct {
	cacheService cache_service.ICacheService
}

type ClearCacheAPIRequest struct {
	ClearAll      bool   `json:"clear_all"`
	Match         string `json:"match"`
	DryRun        bool   `json:"dry_run"`
	LimitMatchKey int    `json:"limitMatchKey"`
}

func NewCacheController(cacheService cache_service.ICacheService) *CacheController {
	return &CacheController{
		cacheService: cacheService,
	}
}

func (cc *CacheController) PingCache() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !validateCacheAPIKey(c) {
			return
		}
		if cc.cacheService == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unhealthy",
				"error":  "cache service unavailable",
			})
			return
		}

		if err := cc.cacheService.PingCache(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unhealthy",
				"error":  err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "healthy",
		})
	}
}

func (cc *CacheController) GetCacheStats() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !validateCacheAPIKey(c) {
			return
		}
		if cc.cacheService == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "cache service unavailable",
			})
			return
		}

		stats, err := cc.cacheService.GetCacheStats(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"stats": stats,
		})
	}
}

func (cc *CacheController) GetCacheValueByKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !validateCacheAPIKey(c) {
			return
		}
		if cc.cacheService == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "cache service unavailable",
			})
			return
		}

		key := strings.TrimSpace(c.Query("key"))
		if key == "" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "query parameter 'key' is required",
			})
			return
		}

		value, err := cc.cacheService.GetValueByKey(c.Request.Context(), key)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": err.Error(),
			})
			return
		}

		if value == "" {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "cache key not found",
				"key":   key,
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"key":   key,
			"value": value,
		})
	}
}

// ClearCache clears redis cache keys either by full clear or by key substring match.
// @Summary Clear Redis cache keys
// @Description Supports full cache clear or key substring match clear with dry-run mode
// @Tags Decision Manager
// @Accept json
// @Produce json
// @Param request body controller.ClearCacheAPIRequest true "Cache clear request"
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string "BadRequest"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 403 {object} map[string]string "Forbidden"
// @Failure 500 {object} map[string]string "InternalServerError"
// @Router /v1/cache/clear [post]
func (cc *CacheController) ClearCache(c *gin.Context) {
	log := commoninit.GetLogger(c.Request.Context())

	if !validateCacheAPIKey(c) {
		return
	}

	if cc.cacheService == nil {
		log.Error("Cache clear service not initialized")
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": "cache clear service unavailable",
		})
		return
	}

	var req ClearCacheAPIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req.Match = strings.TrimSpace(req.Match)
	if req.ClearAll && req.Match != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "provide either clear_all=true or match, not both",
		})
		return
	}
	if !req.ClearAll && req.Match == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "either clear_all=true or a non-empty match is required",
		})
		return
	}
	if req.LimitMatchKey < 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "limitMatchKey must be >= 0",
		})
		return
	}

	result, err := cc.cacheService.ClearCache(c.Request.Context(), cache_service.ClearCacheRequest{
		ClearAll:      req.ClearAll,
		Match:         req.Match,
		DryRun:        req.DryRun,
		LimitMatchKey: req.LimitMatchKey,
	})
	if err != nil {
		log.Errorw("Failed to clear cache keys", "error", err, "clear_all", req.ClearAll, "match", req.Match, "dry_run", req.DryRun)
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": "failed to clear cache keys",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "cache clear completed",
		"result":  result,
	})
}

func validateCacheAPIKey(c *gin.Context) bool {
	token := c.GetHeader(constants.ApiKey)
	expectedKey := commoninit.GetConfigString(constants.ApiKey)
	if expectedKey == "" || len(token) != len(expectedKey) || subtle.ConstantTimeCompare([]byte(token), []byte(expectedKey)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
		return false
	}
	return true
}
