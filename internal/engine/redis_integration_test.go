package engine

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Run with REDIS_INTEGRATION_ADDR=localhost:6379 go test ./internal/engine -run TestRealRedisSharedQuota -count=1.
func TestRealRedisSharedQuota(t *testing.T) {
	addr := os.Getenv("REDIS_INTEGRATION_ADDR")
	if addr == "" {
		t.Skip("set REDIS_INTEGRATION_ADDR to run against real Redis")
	}
	clientA := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 0})
	clientB := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 0})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := clientA.Ping(ctx).Err(); err != nil {
		t.Fatalf("connect to integration Redis: %v", err)
	}
	ruleName := "phase1-integration-" + time.Now().UTC().Format("20060102150405.000000000")
	subject := "api_key:" + ruleName
	rule := Rule{Name: ruleName, Path: "/v1/integration", Capacity: 5, RefillPerSec: 1, KeyTTL: 10 * time.Second, Identity: "api_key"}
	engineA, err := New(clientA, []Rule{rule}, 10*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	engineB, err := New(clientB, []Rule{rule}, 10*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		limiter := engineA
		if i%2 == 1 {
			limiter = engineB
		}
		decision, err := limiter.Check(ctx, rule.Path, subject)
		if err != nil {
			t.Fatalf("check %d: %v", i+1, err)
		}
		if decision.Allowed != (i < 5) {
			t.Fatalf("check %d allowed=%v, want %v", i+1, decision.Allowed, i < 5)
		}
	}
	if ttl := clientA.PTTL(ctx, quotaKey(ruleName, subject)).Val(); ttl <= 0 {
		t.Fatalf("expected positive PTTL for bucket key, got %s", ttl)
	}
}

func TestRealRedisSupportsAllAlgorithmsAcrossEngines(t *testing.T) {
	addr := os.Getenv("REDIS_INTEGRATION_ADDR")
	if addr == "" {
		t.Skip("set REDIS_INTEGRATION_ADDR to run against real Redis")
	}
	clientA := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 0})
	clientB := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 0})
	t.Cleanup(func() { _ = clientA.Close(); _ = clientB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := clientA.Ping(ctx).Err(); err != nil {
		t.Fatalf("connect to integration Redis: %v", err)
	}
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	rules := []Rule{
		{Name: "phase2-token-" + suffix, Path: "/token", Algorithm: AlgorithmTokenBucket, Capacity: 3, RefillPerSec: 1, KeyTTL: time.Minute, Identity: "api_key"},
		{Name: "phase2-fixed-" + suffix, Path: "/fixed", Algorithm: AlgorithmFixedWindow, Capacity: 3, Window: time.Minute, KeyTTL: 2 * time.Minute, Identity: "api_key"},
		{Name: "phase2-sliding-" + suffix, Path: "/sliding", Algorithm: AlgorithmSlidingWindowCounter, Capacity: 3, Window: time.Minute, KeyTTL: 2 * time.Minute, Identity: "api_key"},
	}
	engineA, err := New(clientA, rules, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	engineB, err := New(clientB, rules, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		for i := 0; i < 4; i++ {
			limiter := engineA
			if i%2 != 0 {
				limiter = engineB
			}
			decision, err := limiter.Check(ctx, rule.Path, "api_key:"+suffix)
			if err != nil {
				t.Fatalf("algorithm %s check %d: %v", rule.Algorithm, i+1, err)
			}
			if decision.Allowed != (i < 3) {
				t.Fatalf("algorithm %s check %d allowed=%v, want %v", rule.Algorithm, i+1, decision.Allowed, i < 3)
			}
		}
	}
}
