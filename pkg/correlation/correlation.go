package correlation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// FromContext returns the correlation idHeader that is defined in the context.
func FromContext(ctx context.Context) (string, error) {
	if ctxCorrelationID, ok := ctx.Value(RequestHeader).(string); ok {
		return ctxCorrelationID, nil
	}
	return "", fmt.Errorf("context doesn't have Correlation ID")
}

// NewContext returns a context with a new correlation idHeader if the correlation idHeader does not exist.
func NewContext(correlationId string) (context.Context, error) {
	if correlationId == "" {
		return nil, fmt.Errorf("no Correlation ID provided")
	}
	//nolint
	return context.WithValue(context.Background(), RequestHeader, correlationId), nil
}

// NewId Returns a new Correlation idHeader.
func NewId() string {
	return uuid.NewString()
}
