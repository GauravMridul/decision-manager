package utility

import (
	"context"
	"decision-manager/internal/app/constants"

	"github.com/gin-gonic/gin"
)

// AddIPHeadersToContext extracts the client IP from a Gin context and stores it.
// IMPORTANT: Only call this while the Gin handler is still active (before c.JSON returns).
// For background goroutines, use AddIPToContext with a pre-extracted IP instead.
func AddIPHeadersToContext(c *gin.Context, requestCtx context.Context) context.Context {
	return AddIPToContext(requestCtx, c.ClientIP())
}

// AddIPToContext stores a pre-extracted client IP into the context.
// Safe to use in background goroutines where *gin.Context is no longer valid.
func AddIPToContext(requestCtx context.Context, clientIP string) context.Context {
	return context.WithValue(requestCtx, constants.ForwardedForHeaderKey, clientIP)
}

func ContextForwardedIP(ctx context.Context) string {
	if ctxForwardedIP, ok := ctx.Value(constants.ForwardedForHeaderKey).(string); ok {
		return ctxForwardedIP
	}
	return ""
}
