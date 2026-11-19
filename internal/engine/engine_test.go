package engine

import (
	"context"
	"math"
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

func algorithmEngine(t *testing.T, server *miniredis.Miniredis, rule Rule) *Engine {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	limiter, err := New(client, []Rule{rule}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	return limiter
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

func TestConcurrentChecksAcrossManyEnginesShareOneQuota(t *testing.T) {
	server := miniredis.RunT(t)
	engines := make([]*Engine, 8)
	for i := range engines {
		engines[i], _ = testEngine(t, server.Addr(), 5, time.Second)
	}
	const total = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			limiter := engines[i%len(engines)]
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

func TestTokenBucketMatchesReferenceModelAndDenialsDoNotConsume(t *testing.T) {
	server := miniredis.RunT(t)
	start := time.Unix(1_900_000_000, 0).UTC()
	server.SetTime(start)
	rule := Rule{Name: "reference-token", Path: "/token", Algorithm: AlgorithmTokenBucket, Capacity: 3, RefillPerSec: 2, KeyTTL: 10 * time.Second, Identity: "api_key"}
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	defer client.Close()
	limiter, err := New(client, []Rule{rule}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	type step struct{ after time.Duration }
	steps := []step{{0}, {0}, {100 * time.Millisecond}, {500 * time.Millisecond}, {1500 * time.Millisecond}, {1600 * time.Millisecond}, {2500 * time.Millisecond}}
	var modelTokens float64 = rule.Capacity
	last := start
	for i, item := range steps {
		now := start.Add(item.after)
		server.SetTime(now)
		elapsed := now.Sub(last)
		modelTokens = math.Min(rule.Capacity, modelTokens+float64(elapsed)/float64(time.Second)*rule.RefillPerSec)
		wantAllowed := modelTokens >= 1
		if wantAllowed {
			modelTokens--
		}
		decision, err := limiter.Check(context.Background(), rule.Path, "client")
		if err != nil {
			t.Fatal(err)
		}
		if decision.Allowed != wantAllowed || decision.Remaining != int(math.Floor(modelTokens)) {
			t.Fatalf("step %d at %s: decision=%+v model allowed=%v tokens=%.3f", i, item.after, decision, wantAllowed, modelTokens)
		}
		storedTokens, err := client.HGet(context.Background(), quotaKey(rule.Name, "client"), "tokens").Float64()
		if err != nil {
			t.Fatal(err)
		}
		if storedTokens < 0 || storedTokens > rule.Capacity {
			t.Fatalf("stored tokens out of bounds at step %d: %.3f", i, storedTokens)
		}
		if math.Abs(storedTokens-modelTokens) > 0.001 {
			t.Fatalf("stored tokens at step %d = %.3f, model = %.3f", i, storedTokens, modelTokens)
		}
		if modelTokens < 0 || modelTokens > rule.Capacity {
			t.Fatalf("reference model tokens out of bounds: %.3f", modelTokens)
		}
		last = now
	}
}

func TestFixedWindowMatchesWindowCounterReference(t *testing.T) {
	server := miniredis.RunT(t)
	start := time.Unix(1_900_000_000, 0).UTC()
	server.SetTime(start)
	rule := Rule{Name: "reference-fixed", Path: "/fixed", Algorithm: AlgorithmFixedWindow, Capacity: 3, Window: time.Second, KeyTTL: 3 * time.Second, Identity: "api_key"}
	limiter := algorithmEngine(t, server, rule)
	steps := []time.Duration{0, 100 * time.Millisecond, 999 * time.Millisecond, time.Second, 1001 * time.Millisecond, 1200 * time.Millisecond, 1999 * time.Millisecond, 2 * time.Second}
	counts := map[int64]int{}
	for i, offset := range steps {
		server.SetTime(start.Add(offset))
		windowIndex := int64(offset / time.Second)
		wantAllowed := counts[windowIndex] < int(rule.Capacity)
		if wantAllowed {
			counts[windowIndex]++
		}
		decision, err := limiter.Check(context.Background(), rule.Path, "client")
		if err != nil {
			t.Fatal(err)
		}
		if decision.Allowed != wantAllowed {
			t.Fatalf("step %d at %s: allowed=%v, want %v", i, offset, decision.Allowed, wantAllowed)
		}
		if decision.Allowed && counts[windowIndex] > int(rule.Capacity) {
			t.Fatalf("fixed window over-admitted: %d", counts[windowIndex])
		}
	}
}

func TestSlidingCounterAgainstExactWindowLogOracle(t *testing.T) {
	server := miniredis.RunT(t)
	start := time.Unix(1_900_000_000, 0).UTC()
	server.SetTime(start)
	const limit = 4
	rule := Rule{Name: "sliding-oracle", Path: "/sliding", Algorithm: AlgorithmSlidingWindowCounter, Capacity: limit, Window: time.Second, KeyTTL: 3 * time.Second, Identity: "api_key"}
	limiter := algorithmEngine(t, server, rule)
	events := []time.Duration{950 * time.Millisecond, 950 * time.Millisecond, 950 * time.Millisecond, 950 * time.Millisecond, 1010 * time.Millisecond, 1020 * time.Millisecond, 1951 * time.Millisecond}
	var accepted []time.Duration
	overAdmits := 0
	for i, offset := range events {
		server.SetTime(start.Add(offset))
		cutoff := offset - time.Second
		kept := accepted[:0]
		for _, event := range accepted {
			if event > cutoff {
				kept = append(kept, event)
			}
		}
		accepted = kept
		oracleAllows := len(accepted) < limit
		decision, err := limiter.Check(context.Background(), rule.Path, "client")
		if err != nil {
			t.Fatal(err)
		}
		if decision.Allowed && !oracleAllows {
			overAdmits++
		}
		if oracleAllows && decision.Allowed {
			accepted = append(accepted, offset)
		}
		if !decision.Allowed && !oracleAllows && decision.Remaining < 0 {
			t.Fatalf("negative remaining after denial: %+v", decision)
		}
		if i == 4 && (!decision.Allowed || oracleAllows) {
			t.Fatalf("oracle fixture expected one approximate over-admit at event %d: allowed=%v oracle=%v", i, decision.Allowed, oracleAllows)
		}
	}
	if overAdmits != 1 {
		t.Fatalf("sliding-window counter oracle over-admits = %d, want 1 for documented boundary fixture", overAdmits)
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

func TestLocalFailurePolicyBoundsRequestsPerReplicaDuringRedisOutage(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	defer client.Close()
	rule := Rule{Name: "local-outage", Path: "/local", Capacity: 3, RefillPerSec: 1, KeyTTL: 10 * time.Second, Identity: "api_key", FailurePolicy: FailureLocal}
	limiter, err := New(client, []Rule{rule}, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.SetError("ERR simulated outage")
	for i := 0; i < 4; i++ {
		decision, err := limiter.Check(context.Background(), rule.Path, "same-client")
		if err != nil {
			t.Fatal(err)
		}
		if decision.Allowed != (i < 3) || !decision.Degraded || decision.DegradedPolicy != FailureLocal {
			t.Fatalf("request %d: unexpected local outage decision: %+v", i+1, decision)
		}
	}
}

func TestConcurrentLocalFallbackEnforcesPerReplicaCapacity(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	defer client.Close()
	rule := Rule{Name: "local-concurrent", Path: "/local-concurrent", Capacity: 5, RefillPerSec: 0.1, KeyTTL: time.Minute, Identity: "api_key", FailurePolicy: FailureLocal}
	limiter, err := New(client, []Rule{rule}, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.SetError("ERR simulated outage")

	const requests = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := limiter.Check(context.Background(), rule.Path, "same-client")
			if err != nil {
				t.Errorf("Check: %v", err)
				return
			}
			if decision.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != int(rule.Capacity) {
		t.Fatalf("local fallback allowed %d of %d concurrent outage checks; want %v", allowed, requests, rule.Capacity)
	}
}

func TestFailClosedPolicyDeniesDuringRedisOutage(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	defer client.Close()
	rule := Rule{Name: "closed-outage", Path: "/closed", Capacity: 3, RefillPerSec: 1, KeyTTL: 10 * time.Second, Identity: "api_key", FailurePolicy: FailureClosed}
	limiter, err := New(client, []Rule{rule}, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	server.SetError("ERR simulated outage")
	decision, err := limiter.Check(context.Background(), rule.Path, "same-client")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || !decision.Degraded || decision.DegradedPolicy != FailureClosed {
		t.Fatalf("expected degraded fail-closed denial, got %+v", decision)
	}
}

func TestCircuitBreakerOpensAndAllowsOneRecoveryProbe(t *testing.T) {
	breaker := newCircuitBreaker(2, 20*time.Millisecond)
	breaker.failure()
	if !breaker.allow() {
		t.Fatal("breaker should remain closed before threshold")
	}
	breaker.failure()
	if breaker.allow() {
		t.Fatal("breaker should reject checks while open")
	}
	time.Sleep(25 * time.Millisecond)
	if !breaker.allow() {
		t.Fatal("breaker should allow one half-open probe after cooldown")
	}
	if breaker.allow() {
		t.Fatal("breaker allowed more than one concurrent half-open probe")
	}
	breaker.success()
	if !breaker.allow() {
		t.Fatal("successful probe should close the breaker")
	}
}

func TestRedisInFlightCapUsesConfiguredOutagePolicyWithoutQueueing(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	defer client.Close()
	rule := Rule{Name: "cap", Path: "/cap", Capacity: 2, RefillPerSec: 1, KeyTTL: 10 * time.Second, Identity: "api_key", FailurePolicy: FailureClosed}
	limiter, err := New(client, []Rule{rule}, 100*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(limiter.inFlight); i++ {
		limiter.inFlight <- struct{}{}
	}
	decision, err := limiter.Check(context.Background(), rule.Path, "client")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || !decision.Degraded || decision.DegradedPolicy != FailureClosed {
		t.Fatalf("in-flight cap should apply fail-closed policy immediately: %+v", decision)
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

func TestConfigVersionIsStableAndDetectsRuleDrift(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ruleA := Rule{Name: "a", Path: "/a", Capacity: 5, RefillPerSec: 2, KeyTTL: time.Minute, Identity: "api_key"}
	ruleB := Rule{Name: "b", Path: "/b", Capacity: 10, RefillPerSec: 2, KeyTTL: time.Minute, Identity: "user_id"}
	first, err := New(client, []Rule{ruleA, ruleB}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(client, []Rule{ruleB, ruleA}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigVersion() != second.ConfigVersion() {
		t.Fatalf("rule order changed config version: %s != %s", first.ConfigVersion(), second.ConfigVersion())
	}
	ruleB.Algorithm = AlgorithmFixedWindow
	ruleB.Window = time.Second
	ruleB.RefillPerSec = 0
	third, err := New(client, []Rule{ruleA, ruleB}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigVersion() == third.ConfigVersion() {
		t.Fatal("algorithm change did not change config version")
	}
}
