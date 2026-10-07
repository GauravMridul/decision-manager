package utility

import (
	"context"
	"time"

	"github.com/dmi-infotech/common-modules/go/contracts"
	cmcontext "github.com/dmi-infotech/common-modules/go/utils/context"
)

// Context keys for request-scoped values (use unexported type to avoid collisions)
type dmContextKey struct{ name string }

var (
	requestStartTimeKey = dmContextKey{"requestStartTime"}
	requestLoggerKey    = dmContextKey{"requestLogger"}
	sfRetryLogKey       = dmContextKey{"sfRetryLog"}
)

// SFRetryEvent records a single Salesforce retry attempt for audit purposes.
type SFRetryEvent struct {
	Attempt    int    `json:"attempt"`
	StatusCode int    `json:"statusCode"`
	BackoffMs  int64  `json:"backoffMs"`
	Reason     string `json:"reason"`
}

// SFRetryLog collects retry events within a request. The pointer is placed in
// context once, and callees append to it without allocating a new context.
// Zero-cost on the happy path: the slice stays nil when there are no retries.
type SFRetryLog struct {
	Events []SFRetryEvent
}

// SetSFRetryLog attaches a retry collector to the context.
func SetSFRetryLog(ctx context.Context, rl *SFRetryLog) context.Context {
	return context.WithValue(ctx, sfRetryLogKey, rl)
}

// GetSFRetryLog retrieves the retry collector from context, or nil if not set.
func GetSFRetryLog(ctx context.Context) *SFRetryLog {
	if rl, ok := ctx.Value(sfRetryLogKey).(*SFRetryLog); ok {
		return rl
	}
	return nil
}

// SetRequestStartTime stores the request start time in context
func SetRequestStartTime(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, requestStartTimeKey, t)
}

// GetRequestStartTime returns the request start time from context, or zero value if not set
func GetRequestStartTime(ctx context.Context) time.Time {
	if t, ok := ctx.Value(requestStartTimeKey).(time.Time); ok {
		return t
	}
	return time.Time{}
}

// SetRequestLogger stores the request-scoped logger (with correlation_id, lead_id, stage) in context
func SetRequestLogger(ctx context.Context, logger contracts.Logger) context.Context {
	ctx = context.WithValue(ctx, requestLoggerKey, logger)
	return cmcontext.AddRequestLoggerToContext(ctx, logger)
}

// GetRequestLogger returns the request-scoped logger from context, or nil if not set
func GetRequestLogger(ctx context.Context) contracts.Logger {
	if l := cmcontext.GetRequestLoggerFromContext(ctx); l != nil {
		return l
	}
	if l, ok := ctx.Value(requestLoggerKey).(contracts.Logger); ok {
		return l
	}
	return nil
}
