package correlation

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
)

const (
	RequestHeader string = "X-Correlation-ID"
)

// FromRequest Returns Correlation ID from a gin context.
func FromRequest(c *gin.Context) (string, error) {
	correlationId := c.GetHeader(RequestHeader)
	if correlationId == "" {
		return "", fmt.Errorf("request does not have Correlation Id")
	}
	return correlationId, nil
}

// FromRequestWithKey Returns key and the Correlation ID from a gin context.
func FromRequestWithKey(c *gin.Context) (string, string, error) {
	if correlationID := c.Writer.Header().Get(RequestHeader); correlationID != "" {
		return RequestHeader, correlationID, nil
	}
	return "", "", fmt.Errorf("request does not have Correlation Id")
}

// ContextFromRequest Returns a new context with a correlation ID from an incoming request.
func ContextFromRequest(c *gin.Context) (context.Context, error) {
	correlationId := c.GetHeader(RequestHeader)
	if correlationId == "" {
		return nil, fmt.Errorf("request does not have Correlation Id")
	}
	//nolint
	return context.WithValue(context.Background(), RequestHeader, correlationId), nil
}

func WithReqContext(c *gin.Context) context.Context {
	correlationId := c.GetHeader(RequestHeader)
	if len(correlationId) == 0 {
		correlationId = NewId()
		c.Request.Header.Set(RequestHeader, correlationId)
	}
	c.Writer.Header().Set(RequestHeader, correlationId)

	//nolint
	return context.WithValue(context.Background(), RequestHeader, correlationId)
}
