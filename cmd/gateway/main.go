package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/SanjanaMShetty/distributed-rate-limiter/internal/engine"
	"github.com/SanjanaMShetty/distributed-rate-limiter/internal/observability"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	settings, err := loadSettings()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	redisClient, err := newRedisClient(settings)
	if err != nil {
		logger.Error("invalid Redis configuration", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	metrics := observability.NewMetrics(redisClient)
	limiter, err := engine.New(redisClient, settings.rules, settings.redisTimeout, metrics)
	if err != nil {
		logger.Error("invalid rate-limit engine configuration", "error", err)
		os.Exit(1)
	}
	metrics.SetConfigVersion(limiter.ConfigVersion())
	handler := newHandler(limiter, redisClient, metrics, settings.trustedProxies)
	server := &http.Server{
		Addr: settings.httpAddr, Handler: handler,
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("gateway listening", "addr", settings.httpAddr, "configured_rules", len(settings.rules), "redis_timeout", settings.redisTimeout)
		serverErrors <- server.ListenAndServe()
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		logger.Info("shutting down", "signal", sig.String())
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped", "error", err)
			os.Exit(1)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

type settings struct {
	httpAddr       string
	redisMode      string
	redisAddrs     []string
	redisTimeout   time.Duration
	rules          []engine.Rule
	trustedProxies []*net.IPNet
}

func loadSettings() (settings, error) {
	rules, err := loadRules()
	if err != nil {
		return settings{}, err
	}
	mode := strings.ToLower(env("REDIS_MODE", "single"))
	if mode != "single" && mode != "cluster" {
		return settings{}, errors.New("REDIS_MODE must be single or cluster")
	}
	addrs := strings.Split(env("REDIS_ADDRS", env("REDIS_ADDR", "localhost:6379")), ",")
	for i := range addrs {
		addrs[i] = strings.TrimSpace(addrs[i])
		if addrs[i] == "" {
			return settings{}, errors.New("Redis addresses cannot be empty")
		}
	}
	timeout, err := time.ParseDuration(env("REDIS_TIMEOUT", "10ms"))
	if err != nil || timeout <= 0 {
		return settings{}, errors.New("REDIS_TIMEOUT must be a positive duration")
	}
	trusted, err := parseTrustedProxies(env("TRUSTED_PROXIES", ""))
	if err != nil {
		return settings{}, err
	}
	return settings{httpAddr: env("HTTP_ADDR", ":8080"), redisMode: mode, redisAddrs: addrs,
		redisTimeout: timeout, rules: rules, trustedProxies: trusted}, nil
}

func loadRules() ([]engine.Rule, error) {
	path := env("LIMIT_RULES_FILE", "")
	if path == "" {
		return loadDefaultRule()
	}
	return loadRulesFile(path)
}

func loadDefaultRule() ([]engine.Rule, error) {
	capacity, err := positiveEnvFloat("LIMIT_CAPACITY", 10)
	if err != nil {
		return nil, errors.New("LIMIT_CAPACITY must be a positive number")
	}
	refill, err := positiveEnvFloat("LIMIT_REFILL_PER_SECOND", 2)
	if err != nil {
		return nil, errors.New("LIMIT_REFILL_PER_SECOND must be a positive number")
	}
	ttl, err := positiveEnvDuration("LIMIT_KEY_TTL", "60s")
	if err != nil {
		return nil, errors.New("LIMIT_KEY_TTL must be a positive duration")
	}
	return []engine.Rule{{Name: "default", Path: "/v1/resource", Capacity: capacity, RefillPerSec: refill, KeyTTL: ttl, Identity: "auto"}}, nil
}

func positiveEnvFloat(name string, fallback float64) (float64, error) {
	value, err := envFloat(name, fallback)
	if err != nil || value <= 0 {
		return 0, errors.New("value must be positive")
	}
	return value, nil
}

func positiveEnvDuration(name, fallback string) (time.Duration, error) {
	value, err := time.ParseDuration(env(name, fallback))
	if err != nil || value <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return value, nil
}

type rawRule struct {
	Name          string  `json:"name"`
	Path          string  `json:"path"`
	Capacity      float64 `json:"capacity"`
	RefillPerSec  float64 `json:"refill_per_second"`
	KeyTTL        string  `json:"key_ttl"`
	Algorithm     string  `json:"algorithm"`
	Window        string  `json:"window"`
	Identity      string  `json:"identity"`
	FailurePolicy string  `json:"failure_policy"`
}

func loadRulesFile(path string) ([]engine.Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read LIMIT_RULES_FILE: " + err.Error())
	}
	var rawRules []rawRule
	if err := json.Unmarshal(data, &rawRules); err != nil {
		return nil, errors.New("parse LIMIT_RULES_FILE: " + err.Error())
	}
	if len(rawRules) == 0 {
		return nil, errors.New("LIMIT_RULES_FILE must contain at least one rule")
	}
	rules := make([]engine.Rule, 0, len(rawRules))
	for _, raw := range rawRules {
		rule, err := parseRule(raw)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := validateUniqueRules(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func parseRule(raw rawRule) (engine.Rule, error) {
	ttl, err := time.ParseDuration(raw.KeyTTL)
	if err != nil {
		return engine.Rule{}, errors.New("rule " + raw.Name + ": invalid key_ttl")
	}
	window, err := parseRuleWindow(raw.Name, raw.Window)
	if err != nil {
		return engine.Rule{}, err
	}
	rule := engine.Rule{Name: raw.Name, Path: raw.Path, Capacity: raw.Capacity, RefillPerSec: raw.RefillPerSec, KeyTTL: ttl, Algorithm: raw.Algorithm, Window: window, Identity: raw.Identity, FailurePolicy: raw.FailurePolicy}
	if err := rule.Validate(); err != nil {
		return engine.Rule{}, err
	}
	if isReservedRulePath(rule.Path) {
		return engine.Rule{}, errors.New("rule path must be an exact application path and cannot use a reserved endpoint")
	}
	return rule, nil
}

func parseRuleWindow(name, value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	window, err := time.ParseDuration(value)
	if err != nil {
		return 0, errors.New("rule " + name + ": invalid window")
	}
	return window, nil
}

func isReservedRulePath(path string) bool {
	return path == "/metrics" || path == "/healthz" || path == "/readyz" || strings.Contains(path, "*") || strings.HasSuffix(path, "/")
}

func validateUniqueRules(rules []engine.Rule) error {
	seenPaths, seenNames := make(map[string]bool, len(rules)), make(map[string]bool, len(rules))
	for _, rule := range rules {
		if seenPaths[rule.Path] {
			return errors.New("duplicate rule path: " + rule.Path)
		}
		if seenNames[rule.Name] {
			return errors.New("duplicate rule name: " + rule.Name)
		}
		seenPaths[rule.Path], seenNames[rule.Name] = true, true
	}
	return nil
}

func newRedisClient(cfg settings) (redis.UniversalClient, error) {
	if cfg.redisMode == "cluster" {
		return redis.NewClusterClient(&redis.ClusterOptions{Addrs: cfg.redisAddrs, DialTimeout: cfg.redisTimeout, ReadTimeout: cfg.redisTimeout, WriteTimeout: cfg.redisTimeout, MaxRetries: 0}), nil
	}
	if len(cfg.redisAddrs) != 1 {
		return nil, errors.New("single Redis mode requires exactly one address")
	}
	return redis.NewClient(&redis.Options{Addr: cfg.redisAddrs[0], DialTimeout: cfg.redisTimeout, ReadTimeout: cfg.redisTimeout, WriteTimeout: cfg.redisTimeout, MaxRetries: 0}), nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envFloat(key string, fallback float64) (float64, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseFloat(value, 64)
}
