// Package engine contains the reusable rate-limit decision logic. The HTTP
// gateway only resolves request identity and adapts the engine decision.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis runs this script atomically. Redis TIME keeps refill calculations
// consistent across gateway processes, and PEXPIRE bounds state retention.
const tokenBucketScript = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_per_second = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])

local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local values = redis.call('HMGET', key, 'tokens', 'updated_at_ms')
local tokens = tonumber(values[1])
local updated_at_ms = tonumber(values[2])

if tokens == nil then
  tokens = capacity
  updated_at_ms = now_ms
else
  local elapsed_ms = math.max(0, now_ms - updated_at_ms)
  tokens = math.min(capacity, tokens + elapsed_ms * refill_per_second / 1000)
end

local allowed = 0
local retry_after_ms = 0
if tokens >= 1 then
  allowed = 1
  tokens = tokens - 1
else
  retry_after_ms = math.max(1, math.ceil((1 - tokens) / refill_per_second * 1000))
end
local reset_after_ms = math.max(0, math.ceil((capacity - tokens) / refill_per_second * 1000))

redis.call('HSET', key, 'tokens', tostring(tokens), 'updated_at_ms', tostring(now_ms))
redis.call('PEXPIRE', key, ttl_ms)
return {allowed, math.floor(tokens), retry_after_ms, reset_after_ms}
`

const fixedWindowScript = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local window_index = math.floor(now_ms / window_ms)
local stored_index = tonumber(redis.call('HGET', key, 'window_index'))
local count = tonumber(redis.call('HGET', key, 'count')) or 0
if stored_index ~= window_index then count = 0 end
local allowed = 0
if count < limit then
  count = count + 1
  allowed = 1
  redis.call('HSET', key, 'window_index', tostring(window_index), 'count', tostring(count))
end
local reset_after_ms = window_ms - (now_ms % window_ms)
redis.call('PEXPIRE', key, ttl_ms)
local retry_after_ms = 0
if allowed == 0 then retry_after_ms = reset_after_ms end
return {allowed, math.max(0, limit - count), retry_after_ms, reset_after_ms}
`

const slidingWindowCounterScript = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local ttl_ms = tonumber(ARGV[3])
local now = redis.call('TIME')
local now_ms = tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
local window_index = math.floor(now_ms / window_ms)
local elapsed_ms = now_ms % window_ms
local stored_index = tonumber(redis.call('HGET', key, 'window_index'))
local stored_count = tonumber(redis.call('HGET', key, 'count')) or 0
local stored_previous_index = tonumber(redis.call('HGET', key, 'previous_index'))
local stored_previous_count = tonumber(redis.call('HGET', key, 'previous_count')) or 0

local current_count = 0
local previous_count = 0
if stored_index == window_index then
  current_count = stored_count
  if stored_previous_index == window_index - 1 then previous_count = stored_previous_count end
elseif stored_index == window_index - 1 then
  previous_count = stored_count
end

local previous_weight = 1 - (elapsed_ms / window_ms)
local estimate = current_count + previous_count * previous_weight
local allowed = 0
if estimate < limit then
  current_count = current_count + 1
  allowed = 1
end

redis.call('HSET', key,
  'window_index', tostring(window_index),
  'count', tostring(current_count),
  'previous_index', tostring(window_index - 1),
  'previous_count', tostring(previous_count))
redis.call('PEXPIRE', key, ttl_ms)

local remaining = math.max(0, math.floor(limit - estimate - allowed))
local reset_after_ms = window_ms - elapsed_ms
local retry_after_ms = 0
if allowed == 0 then
  retry_after_ms = reset_after_ms
  if previous_count > 0 and current_count < limit then
    local wait_ms = math.floor((estimate - limit) * window_ms / previous_count) + 1
    if wait_ms > 0 and wait_ms < reset_after_ms then
      retry_after_ms = wait_ms
    elseif current_count >= limit then
      retry_after_ms = reset_after_ms + 1
    end
  elseif current_count >= limit then
    retry_after_ms = reset_after_ms + 1
  end
end
return {allowed, remaining, retry_after_ms, reset_after_ms}
`

const (
	AlgorithmTokenBucket          = "token_bucket"
	AlgorithmFixedWindow          = "fixed_window"
	AlgorithmSlidingWindowCounter = "sliding_window_counter"
)

const (
	FailureOpen   = "fail_open"
	FailureClosed = "fail_closed"
	FailureLocal  = "local"
)

type Rule struct {
	Name          string        `json:"name"`
	Path          string        `json:"path"`
	Capacity      float64       `json:"capacity"`
	RefillPerSec  float64       `json:"refill_per_second"`
	KeyTTL        time.Duration `json:"-"`
	Algorithm     string        `json:"algorithm"`
	Window        time.Duration `json:"-"`
	Identity      string        `json:"identity"`
	FailurePolicy string        `json:"failure_policy,omitempty"`
}

func (r Rule) Validate() error {
	if strings.TrimSpace(r.Name) == "" || !strings.HasPrefix(r.Path, "/") || r.Path == "/" {
		return errors.New("each rule requires a name and a non-root absolute path")
	}
	if r.Capacity <= 0 || r.Capacity > float64(math.MaxInt32) || r.KeyTTL <= 0 {
		return fmt.Errorf("rule %q requires positive capacity and key_ttl", r.Name)
	}
	algorithm := r.Algorithm
	if algorithm == "" {
		algorithm = AlgorithmTokenBucket
	}
	if err := validateAlgorithm(r, algorithm); err != nil {
		return err
	}
	if err := validateIdentity(r); err != nil {
		return err
	}
	return validateFailurePolicy(r, algorithm)
}

func validateAlgorithm(r Rule, algorithm string) error {
	switch algorithm {
	case AlgorithmTokenBucket:
		if r.RefillPerSec <= 0 {
			return fmt.Errorf("rule %q requires positive refill_per_second", r.Name)
		}
		if float64(r.KeyTTL)/float64(time.Second) < r.Capacity/r.RefillPerSec {
			return fmt.Errorf("rule %q key_ttl must be at least the full bucket refill time", r.Name)
		}
	case AlgorithmFixedWindow, AlgorithmSlidingWindowCounter:
		if r.Window < time.Millisecond {
			return fmt.Errorf("rule %q requires a window of at least 1ms", r.Name)
		}
		if math.Trunc(r.Capacity) != r.Capacity {
			return fmt.Errorf("rule %q requires an integer capacity for window algorithms", r.Name)
		}
		if r.KeyTTL < 2*r.Window {
			return fmt.Errorf("rule %q key_ttl must be at least twice the window", r.Name)
		}
	default:
		return fmt.Errorf("rule %q uses unsupported algorithm %q", r.Name, algorithm)
	}
	return nil
}

func validateIdentity(r Rule) error {
	switch r.Identity {
	case "auto", "api_key", "user_id", "ip":
	default:
		return fmt.Errorf("rule %q identity must be auto, api_key, user_id, or ip", r.Name)
	}
	return nil
}

func validateFailurePolicy(r Rule, algorithm string) error {
	policy := r.FailurePolicy
	if policy == "" {
		policy = FailureOpen
	}
	if policy != FailureOpen && policy != FailureClosed && policy != FailureLocal {
		return fmt.Errorf("rule %q failure_policy must be fail_open, fail_closed, or local", r.Name)
	}
	if policy == FailureLocal && algorithm != AlgorithmTokenBucket {
		return fmt.Errorf("rule %q local failure policy currently requires token_bucket algorithm", r.Name)
	}
	return nil
}

type Decision struct {
	Allowed        bool          `json:"allowed"`
	Degraded       bool          `json:"degraded,omitempty"`
	DegradedPolicy string        `json:"degraded_policy,omitempty"`
	Limit          int           `json:"limit"`
	Remaining      int           `json:"remaining"`
	RetryAfter     time.Duration `json:"-"`
	ResetAfter     time.Duration `json:"-"`
}

type Observer interface {
	ObserveDecision(outcome string, elapsed time.Duration)
	ObserveStoreError()
}

type Engine struct {
	client   redis.UniversalClient
	rules    map[string]Rule
	timeout  time.Duration
	observer Observer
	fallback *localFallback
	breaker  *circuitBreaker
	inFlight chan struct{}
}

const (
	defaultBreakerThreshold = 5
	defaultBreakerOpenFor   = time.Second
	defaultMaxRedisInFlight = 256
)

func New(client redis.UniversalClient, rules []Rule, timeout time.Duration, observer Observer) (*Engine, error) {
	if client == nil {
		return nil, errors.New("Redis client is required")
	}
	if timeout <= 0 {
		return nil, errors.New("Redis timeout must be positive")
	}
	if len(rules) == 0 {
		return nil, errors.New("at least one rate-limit rule is required")
	}
	byPath := make(map[string]Rule, len(rules))
	byName := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if err := rule.Validate(); err != nil {
			return nil, err
		}
		if _, exists := byPath[rule.Path]; exists {
			return nil, fmt.Errorf("duplicate rule path %q", rule.Path)
		}
		if byName[rule.Name] {
			return nil, fmt.Errorf("duplicate rule name %q", rule.Name)
		}
		if rule.Algorithm == "" {
			rule.Algorithm = AlgorithmTokenBucket
		}
		if rule.FailurePolicy == "" {
			rule.FailurePolicy = FailureOpen
		}
		byPath[rule.Path] = rule
		byName[rule.Name] = true
	}
	return &Engine{
		client: client, rules: byPath, timeout: timeout, observer: observer,
		fallback: newLocalFallback(10000, 5*time.Minute),
		breaker:  newCircuitBreaker(defaultBreakerThreshold, defaultBreakerOpenFor),
		inFlight: make(chan struct{}, defaultMaxRedisInFlight),
	}, nil
}

func (e *Engine) Rules() []Rule {
	rules := make([]Rule, 0, len(e.rules))
	for _, rule := range e.rules {
		rules = append(rules, rule)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Path < rules[j].Path })
	return rules
}

func (e *Engine) ConfigVersion() string {
	parts := make([]string, 0, len(e.rules))
	for _, rule := range e.Rules() {
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%g|%g|%s|%s|%d|%s", rule.Name, rule.Path, rule.Algorithm, rule.Capacity, rule.RefillPerSec, rule.Identity, rule.KeyTTL, rule.Window, rule.FailurePolicy))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(digest[:8])
}

// Check resolves the path to a rule and atomically consumes one token. Redis
// Redis failures are bounded by the configured timeout and fail open with a
// marked decision. A canceled caller context is still returned as an error.
func (e *Engine) Check(ctx context.Context, path, subject string) (Decision, error) {
	rule, ok := e.rules[path]
	if !ok {
		return Decision{}, fmt.Errorf("no rate-limit rule for path %q", path)
	}
	if strings.TrimSpace(subject) == "" || len(subject) > 256 {
		return Decision{}, errors.New("subject must contain between 1 and 256 bytes")
	}
	key := quotaKey(rule.Name, subject)
	started := time.Now()
	if !e.acquireRedisSlot() {
		return e.recordDegraded(rule, key, started), nil
	}
	defer e.releaseRedisSlot()
	if !e.breaker.allow() {
		e.observeCircuitOpen()
		return e.recordDegraded(rule, key, started), nil
	}
	return e.checkRedis(ctx, rule, key, started)
}

func (e *Engine) acquireRedisSlot() bool {
	select {
	case e.inFlight <- struct{}{}:
		return true
	default:
		if observer, ok := e.observer.(interface{ ObserveRedisInFlightRejected() }); ok {
			observer.ObserveRedisInFlightRejected()
		}
		return false
	}
}

func (e *Engine) releaseRedisSlot() { <-e.inFlight }

func (e *Engine) observeCircuitOpen() {
	if observer, ok := e.observer.(interface{ ObserveCircuitOpen() }); ok {
		observer.ObserveCircuitOpen()
	}
}

func (e *Engine) checkRedis(ctx context.Context, rule Rule, key string, started time.Time) (Decision, error) {
	checkCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	script, args, err := scriptForRule(rule)
	if err != nil {
		return Decision{}, err
	}
	result, err := redis.NewScript(script).Run(checkCtx, e.client, []string{key}, args...).Result()
	elapsed := time.Since(started)
	if err != nil {
		if e.observer != nil {
			e.observer.ObserveStoreError()
		}
		if ctx.Err() != nil {
			e.breaker.abandonProbe()
			return Decision{}, ctx.Err()
		}
		e.breaker.failure()
		return e.recordDegraded(rule, key, started), nil
	}
	e.breaker.success()
	return decisionFromRedisResult(result, rule, elapsed, e.observer)
}

func scriptForRule(rule Rule) (string, []interface{}, error) {
	args := []interface{}{rule.Capacity, rule.Window.Milliseconds(), rule.KeyTTL.Milliseconds()}
	switch rule.Algorithm {
	case AlgorithmTokenBucket:
		return tokenBucketScript, []interface{}{rule.Capacity, rule.RefillPerSec, rule.KeyTTL.Milliseconds()}, nil
	case AlgorithmFixedWindow:
		return fixedWindowScript, args, nil
	case AlgorithmSlidingWindowCounter:
		return slidingWindowCounterScript, args, nil
	default:
		return "", nil, fmt.Errorf("unsupported rate-limit algorithm %q", rule.Algorithm)
	}
}

func decisionFromRedisResult(result interface{}, rule Rule, elapsed time.Duration, observer Observer) (Decision, error) {
	values, ok := result.([]interface{})
	if !ok || len(values) != 4 {
		return Decision{}, errors.New("unexpected Redis script response")
	}
	allowed, err := redisInt(values[0])
	if err != nil {
		return Decision{}, err
	}
	remaining, err := redisInt(values[1])
	if err != nil {
		return Decision{}, err
	}
	retryMS, err := redisInt(values[2])
	if err != nil {
		return Decision{}, err
	}
	resetMS, err := redisInt(values[3])
	if err != nil {
		return Decision{}, err
	}
	outcome := "deny"
	if allowed == 1 {
		outcome = "allow"
	}
	if observer != nil {
		observer.ObserveDecision(outcome, elapsed)
	}
	return Decision{
		Allowed: allowed == 1, Limit: int(math.Ceil(rule.Capacity)), Remaining: int(remaining),
		RetryAfter: time.Duration(retryMS) * time.Millisecond, ResetAfter: time.Duration(resetMS) * time.Millisecond,
	}, nil
}

func (e *Engine) recordDegraded(rule Rule, key string, started time.Time) Decision {
	decision := e.degradedDecision(rule, key)
	if e.observer != nil {
		outcome := "deny_degraded"
		if decision.Allowed {
			outcome = "allow_degraded"
		}
		e.observer.ObserveDecision(outcome, time.Since(started))
		if policyObserver, ok := e.observer.(interface{ ObserveDegradedPolicy(policy, decision string) }); ok {
			degradedDecision := "deny"
			if decision.Allowed {
				degradedDecision = "allow"
			}
			policyObserver.ObserveDegradedPolicy(rule.FailurePolicy, degradedDecision)
		}
		if failOpenObserver, ok := e.observer.(interface{ ObserveFailOpen() }); ok && rule.FailurePolicy == FailureOpen && decision.Allowed {
			failOpenObserver.ObserveFailOpen()
		}
	}
	return decision
}

func (e *Engine) degradedDecision(rule Rule, key string) Decision {
	decision := Decision{Degraded: true, DegradedPolicy: rule.FailurePolicy, Limit: int(math.Ceil(rule.Capacity)), Remaining: -1}
	switch rule.FailurePolicy {
	case FailureClosed:
		decision.Allowed = false
	case FailureLocal:
		local := e.fallback.check(key, rule.Capacity, rule.RefillPerSec)
		decision.Allowed, decision.Remaining = local.allowed, local.remaining
		decision.RetryAfter, decision.ResetAfter = local.retryAfter, local.resetAfter
	default:
		decision.Allowed = true
	}
	return decision
}

func quotaKey(ruleName, subject string) string {
	digest := sha256.Sum256([]byte(ruleName + ":" + subject))
	return "rl:tb:" + hex.EncodeToString(digest[:])
}

func redisInt(value interface{}) (int64, error) {
	switch number := value.(type) {
	case int64:
		return number, nil
	case string:
		var parsed int64
		_, err := fmt.Sscan(number, &parsed)
		return parsed, err
	default:
		return 0, fmt.Errorf("unexpected Redis integer type %T", value)
	}
}
