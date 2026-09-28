package llm

import (
	"context"
	"time"
)

// RetryInfo describes a request the adapter is about to retry.
type RetryInfo struct {
	Attempt     int     `json:"attempt"`
	WaitSeconds float64 `json:"wait_seconds"`
	Reason      string  `json:"reason"`
}

type retryHookKey struct{}

// WithRetryHook returns a context whose LLM calls report retries to fn, in the
// style of net/http/httptrace. Callers use it to surface rate limiting to users.
func WithRetryHook(ctx context.Context, fn func(RetryInfo)) context.Context {
	return context.WithValue(ctx, retryHookKey{}, fn)
}

func notifyRetry(ctx context.Context, attempt int, wait time.Duration, reason string) {
	if fn, ok := ctx.Value(retryHookKey{}).(func(RetryInfo)); ok {
		fn(RetryInfo{Attempt: attempt, WaitSeconds: wait.Seconds(), Reason: reason})
	}
}
