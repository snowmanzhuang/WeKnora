package im

import (
	"context"
	"testing"

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
