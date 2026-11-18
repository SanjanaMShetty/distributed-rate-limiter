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

type Rule struct {
	Name         string        `json:"name"`
	Path         string        `json:"path"`
	Capacity     float64       `json:"capacity"`
	RefillPerSec float64       `json:"refill_per_second"`
	KeyTTL       time.Duration `json:"-"`
	Identity     string        `json:"identity"`
}

func (r Rule) Validate() error {
	if strings.TrimSpace(r.Name) == "" || !strings.HasPrefix(r.Path, "/") || r.Path == "/" {
		return errors.New("each rule requires a name and a non-root absolute path")
	}
	if r.Capacity <= 0 || r.Capacity > float64(math.MaxInt32) || r.RefillPerSec <= 0 || r.KeyTTL <= 0 {
		return fmt.Errorf("rule %q requires positive capacity, refill_per_second, and key_ttl", r.Name)
	}
	// A shorter TTL would delete a depleted bucket before it refills, allowing
	// an early reset and accidental over-admission.
	if float64(r.KeyTTL)/float64(time.Second) < r.Capacity/r.RefillPerSec {
		return fmt.Errorf("rule %q key_ttl must be at least the full bucket refill time", r.Name)
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
	return rules
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
	result, err := redis.NewScript(tokenBucketScript).Run(checkCtx, e.client, []string{key},
		rule.Capacity, rule.RefillPerSec, rule.KeyTTL.Milliseconds()).Result()
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
