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

type Rule struct {
	Name         string        `json:"name"`
	Path         string        `json:"path"`
	Capacity     float64       `json:"capacity"`
	RefillPerSec float64       `json:"refill_per_second"`
	KeyTTL       time.Duration `json:"-"`
	Algorithm    string        `json:"algorithm"`
	Window       time.Duration `json:"-"`
	Identity     string        `json:"identity"`
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
	switch r.Identity {
	case "auto", "api_key", "user_id", "ip":
		return nil
	default:
		return fmt.Errorf("rule %q identity must be auto, api_key, user_id, or ip", r.Name)
	}
}

type Decision struct {
	Allowed    bool          `json:"allowed"`
	Degraded   bool          `json:"degraded,omitempty"`
	Limit      int           `json:"limit"`
	Remaining  int           `json:"remaining"`
	RetryAfter time.Duration `json:"-"`
	ResetAfter time.Duration `json:"-"`
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
}

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
		byPath[rule.Path] = rule
		byName[rule.Name] = true
	}
	return &Engine{client: client, rules: byPath, timeout: timeout, observer: observer}, nil
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
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%g|%g|%s|%s|%d", rule.Name, rule.Path, rule.Algorithm, rule.Capacity, rule.RefillPerSec, rule.Identity, rule.KeyTTL, rule.Window))
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
	checkCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	started := time.Now()
	var script string
	var args []interface{}
	switch rule.Algorithm {
	case AlgorithmTokenBucket:
		script = tokenBucketScript
		args = []interface{}{rule.Capacity, rule.RefillPerSec, rule.KeyTTL.Milliseconds()}
	case AlgorithmFixedWindow:
		script = fixedWindowScript
		args = []interface{}{rule.Capacity, rule.Window.Milliseconds(), rule.KeyTTL.Milliseconds()}
	case AlgorithmSlidingWindowCounter:
		script = slidingWindowCounterScript
		args = []interface{}{rule.Capacity, rule.Window.Milliseconds(), rule.KeyTTL.Milliseconds()}
	default:
		return Decision{}, fmt.Errorf("unsupported rate-limit algorithm %q", rule.Algorithm)
	}
	result, err := redis.NewScript(script).Run(checkCtx, e.client, []string{key}, args...).Result()
	elapsed := time.Since(started)
	if err != nil {
		if e.observer != nil {
			e.observer.ObserveStoreError()
		}
		if ctx.Err() != nil {
			return Decision{}, ctx.Err()
		}
		if e.observer != nil {
			e.observer.ObserveDecision("allow_degraded", elapsed)
			if failOpenObserver, ok := e.observer.(interface{ ObserveFailOpen() }); ok {
				failOpenObserver.ObserveFailOpen()
			}
		}
		return Decision{Allowed: true, Degraded: true, Limit: int(math.Ceil(rule.Capacity)), Remaining: -1}, nil
	}
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
	if e.observer != nil {
		e.observer.ObserveDecision(outcome, elapsed)
	}
	return Decision{
		Allowed: allowed == 1, Limit: int(math.Ceil(rule.Capacity)), Remaining: int(remaining),
		RetryAfter: time.Duration(retryMS) * time.Millisecond, ResetAfter: time.Duration(resetMS) * time.Millisecond,
	}, nil
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
