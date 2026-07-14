package chat

import (
	"context"
	"errors"
	"testing"
)

func TestShouldFailover(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "unauthorized", err: errors.New("API request failed with status 401"), want: true},
		{name: "model missing", err: errors.New("API request failed with status 404"), want: true},
		{name: "rate limited", err: errors.New("API request failed with status 429"), want: true},
		{name: "server error", err: errors.New("API request failed with status 503"), want: true},
		{name: "bad request", err: errors.New("API request failed with status 400: invalid request"), want: false},
		{name: "context length", err: errors.New("maximum context length exceeded"), want: false},
		{name: "canceled", err: context.Canceled, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldFailover(context.Background(), tt.err); got != tt.want {
				t.Fatalf("ShouldFailover() = %v, want %v", got, tt.want)
			}
		})
	}
}
