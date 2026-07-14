package chat

import (
	"context"
	"errors"
	"strings"
)

// ShouldFailover reports whether retrying the same request with another model
// can plausibly recover. Request-shape errors are deliberately excluded because
// they would normally fail identically on every configured model.
func ShouldFailover(ctx context.Context, err error) bool {
	if err == nil || (ctx != nil && ctx.Err() != nil) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	lower := strings.ToLower(err.Error())
	for _, marker := range []string{
		"status 400", "http status: 400",
		"status 413", "http status: 413",
		"status 422", "http status: 422",
		"context length", "maximum context", "max context",
		"invalid request", "invalid_request_error",
		"content filter", "content_filter",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}
