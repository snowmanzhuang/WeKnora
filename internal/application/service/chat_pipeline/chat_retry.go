package chatpipeline

import (
	"context"
	"errors"
	"strings"
	"time"
)

const chatCompletionMaxAttempts = 3

var chatCompletionRetrySleeper = func(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func chatCompletionRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(attempt) * time.Second
}

func sleepBeforeChatRetry(ctx context.Context, attempt int) bool {
	return chatCompletionRetrySleeper(ctx, chatCompletionRetryDelay(attempt))
}

func isRetryableChatModelError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	msg := err.Error()
	lower := strings.ToLower(msg)
	for _, marker := range []string{
		"status 408", "status 429",
		"status 500", "status 501", "status 502", "status 503", "status 504",
		"status 520", "status 521", "status 522", "status 523", "status 524",
		"http status: 408", "http status: 429",
		"http status: 500", "http status: 501", "http status: 502", "http status: 503", "http status: 504",
		"http status: 520", "http status: 521", "http status: 522", "http status: 523", "http status: 524",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	for _, marker := range []string{
		"timeout",
		"timed out",
		"connection reset",
		"connection refused",
		"broken pipe",
		"no such host",
		"i/o timeout",
		"unexpected eof",
		"tls handshake",
		"context deadline exceeded",
		"temporarily unavailable",
		"service unavailable",
		"rate limit",
		"too many requests",
		"bad gateway",
		"gateway timeout",
		"server error",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
