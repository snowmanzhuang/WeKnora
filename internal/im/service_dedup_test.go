package im

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestIsDuplicateScopesMessagesByChannelInMemory(t *testing.T) {
	svc := &Service{}
	ctx := context.Background()

	if svc.isDuplicate(ctx, "channel-retina", "message-1") {
		t.Fatal("first delivery was treated as a duplicate")
	}
	if !svc.isDuplicate(ctx, "channel-retina", "message-1") {
		t.Fatal("second delivery to the same channel was not treated as a duplicate")
	}
	if svc.isDuplicate(ctx, "channel-cataract", "message-1") {
		t.Fatal("same message delivered to another channel was treated as a duplicate")
	}
}

func TestIsDuplicateScopesMessagesByChannelInRedis(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	svc := &Service{redis: client}
	ctx := context.Background()

	if svc.isDuplicate(ctx, "channel-retina", "message-1") {
		t.Fatal("first delivery was treated as a duplicate")
	}
	if !svc.isDuplicate(ctx, "channel-retina", "message-1") {
		t.Fatal("second delivery to the same channel was not treated as a duplicate")
	}
	if svc.isDuplicate(ctx, "channel-cataract", "message-1") {
		t.Fatal("same message delivered to another channel was treated as a duplicate")
	}
}

func TestIsDuplicateIgnoresCanceledCallbackContext(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	svc := &Service{redis: client}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if svc.isDuplicate(ctx, "channel-retina", "message-canceled-context") {
		t.Fatal("first delivery was dropped because its callback context was canceled")
	}
	if !svc.isDuplicate(ctx, "channel-retina", "message-canceled-context") {
		t.Fatal("second delivery was not caught by local deduplication")
	}

	if !server.Exists(RedisKeyDedup + "channel-retina:message-canceled-context") {
		t.Fatal("dedup key was not written to Redis with a canceled callback context")
	}
}

func TestIsDuplicateFallsBackLocallyWhenRedisUnavailable(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
		MaxRetries:   -1,
	})
	t.Cleanup(func() { _ = client.Close() })

	svc := &Service{redis: client}
	ctx := context.Background()

	if svc.isDuplicate(ctx, "channel-retina", "message-redis-down") {
		t.Fatal("first delivery was dropped when Redis was unavailable")
	}
	if !svc.isDuplicate(ctx, "channel-retina", "message-redis-down") {
		t.Fatal("second delivery was not caught by local fallback deduplication")
	}
}
