package engine

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func testEngine(t *testing.T, addr string, capacity float64, timeout time.Duration) (*Engine, *redis.Client) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: timeout, ReadTimeout: timeout, WriteTimeout: timeout, MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	rule := Rule{Name: "test-rule", Path: "/v1/test", Capacity: capacity, RefillPerSec: 2, KeyTTL: time.Minute, Identity: "api_key"}
	limiter, err := New(client, []Rule{rule}, timeout, nil)
	if err != nil {
		t.Fatal(err)
	}
	return limiter, client
}

func TestCheckEnforcesCapacityAndSetsTTL(t *testing.T) {
	server := miniredis.RunT(t)
	limiter, _ := testEngine(t, server.Addr(), 5, 100*time.Millisecond)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		decision, err := limiter.Check(ctx, "/v1/test", "api_key:capacity-check")
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Allowed || decision.Remaining != 4-i {
			t.Fatalf("request %d: got %+v, want allowed with remaining %d", i+1, decision, 4-i)
		}
	}
	decision, err := limiter.Check(ctx, "/v1/test", "api_key:capacity-check")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Remaining != 0 || decision.RetryAfter <= 0 {
		t.Fatalf("expected exhausted bucket, got %+v", decision)
	}
	if ttl := server.TTL(quotaKey("test-rule", "api_key:capacity-check")); ttl <= 0 {
		t.Fatalf("expected positive key TTL, got %s", ttl)
	}
}

func TestConcurrentChecksAcrossEnginesShareOneQuota(t *testing.T) {
	server := miniredis.RunT(t)
	first, _ := testEngine(t, server.Addr(), 5, time.Second)
	second, _ := testEngine(t, server.Addr(), 5, time.Second)
	const total = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			limiter := first
			if i%2 != 0 {
				limiter = second
			}
			decision, err := limiter.Check(context.Background(), "/v1/test", "api_key:concurrent")
			if err != nil {
				t.Errorf("Check: %v", err)
				return
			}
			if decision.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if allowed != 5 {
		t.Fatalf("allowed %d of %d concurrent checks; want exactly 5", allowed, total)
	}
}

func TestRedisErrorReturnsWithinConfiguredBudget(t *testing.T) {
	server := miniredis.RunT(t)
	limiter, _ := testEngine(t, server.Addr(), 5, 75*time.Millisecond)
	server.SetError("ERR simulated Redis outage")
	started := time.Now()
	decision, err := limiter.Check(context.Background(), "/v1/test", "api_key:outage")
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("expected fail-open decision, got error %v", err)
	}
	if !decision.Allowed || !decision.Degraded || decision.Remaining != -1 {
		t.Fatalf("expected marked fail-open decision, got %+v", decision)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("Redis error took %s; expected bounded response", elapsed)
	}
}

func TestStalledRedisConnectionTimesOutAndFailsOpen(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-release
	}()

	const budget = 75 * time.Millisecond
	limiter, _ := testEngine(t, listener.Addr().String(), 5, budget)
	started := time.Now()
	decision, err := limiter.Check(context.Background(), "/v1/test", "api_key:stalled")
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("expected fail-open timeout decision, got error %v", err)
	}
	if !decision.Allowed || !decision.Degraded {
		t.Fatalf("expected marked degraded allow, got %+v", decision)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("stalled Redis exceeded the decision budget: %s", elapsed)
	}
}

func TestCheckRejectsUnknownRuleAndTooShortTTL(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	_, err := New(client, []Rule{{Name: "bad", Path: "/bad", Capacity: 10, RefillPerSec: 2, KeyTTL: time.Second, Identity: "api_key"}}, time.Second, nil)
	if err == nil {
		t.Fatal("expected rule TTL validation error")
	}
	valid := Rule{Name: "valid", Path: "/v1/test", Capacity: 5, RefillPerSec: 2, KeyTTL: time.Minute, Identity: "api_key"}
	limiter, err := New(client, []Rule{valid}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Check(context.Background(), "/not-configured", "api_key:test"); err == nil {
		t.Fatal("expected unknown rule error")
	}
}

func TestIndependentRulesUseIndependentKeys(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	rules := []Rule{
		{Name: "one", Path: "/one", Capacity: 1, RefillPerSec: 1, KeyTTL: 2 * time.Second, Identity: "api_key"},
		{Name: "two", Path: "/two", Capacity: 1, RefillPerSec: 1, KeyTTL: 2 * time.Second, Identity: "api_key"},
	}
	limiter, err := New(client, rules, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/one", "/two"} {
		decision, err := limiter.Check(context.Background(), path, "api_key:same-client")
		if err != nil || !decision.Allowed {
			t.Fatalf("rule %s should start with its own full bucket: decision=%+v err=%v", path, decision, err)
		}
	}
}
