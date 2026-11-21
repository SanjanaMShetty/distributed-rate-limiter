package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SanjanaMShetty/distributed-rate-limiter/internal/engine"
	"github.com/SanjanaMShetty/distributed-rate-limiter/internal/observability"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

func newHandler(limiter *engine.Engine, redisClient redis.UniversalClient, metrics *observability.Metrics, trustedProxies []*net.IPNet) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Millisecond)
		defer cancel()
		status := "available"
		if err := redisClient.Ping(ctx).Err(); err != nil {
			status = "degraded_fail_open"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ready": true, "redis": status})
	})
	for _, rule := range limiter.Rules() {
		rule := rule
		mux.Handle(rule.Path, rateLimitHandler(limiter, rule, trustedProxies, metrics, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			decision, _ := decisionFromContext(r)
			w.Header().Set("Content-Type", "application/json")
			body := map[string]interface{}{"allowed": true, "limit": decision.Limit, "message": "request allowed"}
			if decision.Degraded {
				body["degraded"] = true
				if decision.DegradedPolicy == engine.FailureLocal {
					body["remaining"], body["reset_at"] = decision.Remaining, resetTime(decision.ResetAfter)
				} else {
					body["remaining"], body["reset_at"] = nil, nil
				}
			} else {
				body["remaining"], body["reset_at"] = decision.Remaining, resetTime(decision.ResetAfter)
			}
			_ = json.NewEncoder(w).Encode(body)
		})))
	}
	return mux
}

func rateLimitHandler(limiter *engine.Engine, rule engine.Rule, trustedProxies []*net.IPNet, metrics *observability.Metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		subject, err := resolveIdentity(r, rule.Identity, trustedProxies)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "client_identity_required", err.Error())
			metrics.ObserveHTTP(rule.Path, http.StatusUnauthorized, time.Since(started))
			return
		}
		decision, err := limiter.Check(r.Context(), rule.Path, subject)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "rate_limiter_unavailable", "rate limiter unavailable; request was not forwarded")
			metrics.ObserveHTTP(rule.Path, http.StatusServiceUnavailable, time.Since(started))
			return
		}
		if !writeDecisionHeaders(w, decision) {
			writeError(w, http.StatusServiceUnavailable, "rate_limiter_unavailable", "rate limiter unavailable; request was not forwarded")
			metrics.ObserveHTTP(rule.Path, http.StatusServiceUnavailable, time.Since(started))
			return
		}
		if !decision.Allowed {
			writeRateLimitExceeded(w, decision)
			metrics.ObserveHTTP(rule.Path, http.StatusTooManyRequests, time.Since(started))
			return
		}
		if !decision.Degraded {
			w.Header().Set("Retry-After", "0")
		}
		r = r.WithContext(context.WithValue(r.Context(), decisionContextKey{}, decision))
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		metrics.ObserveHTTP(rule.Path, wrapped.status, time.Since(started))
	})
}

func writeDecisionHeaders(w http.ResponseWriter, decision engine.Decision) bool {
	w.Header().Set("RateLimit-Limit", strconv.Itoa(decision.Limit))
	if !decision.Degraded {
		writeQuotaHeaders(w, decision)
		return true
	}

	degradedHeader := decision.DegradedPolicy
	if degradedHeader == engine.FailureOpen {
		degradedHeader = "fail-open"
	}
	w.Header().Set("RateLimit-Degraded", degradedHeader)
	if decision.DegradedPolicy == engine.FailureClosed {
		return false
	}
	if decision.DegradedPolicy == engine.FailureLocal {
		writeQuotaHeaders(w, decision)
	}
	return true
}

func writeQuotaHeaders(w http.ResponseWriter, decision engine.Decision) {
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(decision.Remaining))
	w.Header().Set("RateLimit-Reset", strconv.FormatInt(resetSeconds(decision.ResetAfter), 10))
}

func writeRateLimitExceeded(w http.ResponseWriter, decision engine.Decision) {
	resetAt := time.Now().Add(decision.ResetAfter)
	seconds := int(math.Ceil(decision.RetryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"allowed": false, "error": "rate_limit_exceeded", "message": "request limit exceeded",
		"limit": decision.Limit, "remaining": decision.Remaining,
		"retry_after_seconds": seconds, "reset_at": resetAt.UTC().Format(time.RFC3339Nano),
	})
}

type decisionContextKey struct{}

func decisionFromContext(r *http.Request) (engine.Decision, bool) {
	decision, ok := r.Context().Value(decisionContextKey{}).(engine.Decision)
	return decision, ok
}

func resolveIdentity(r *http.Request, mode string, trustedProxies []*net.IPNet) (string, error) {
	apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
	clientID := strings.TrimSpace(r.Header.Get("X-Client-ID"))
	if mode == "auto" {
		if apiKey != "" {
			return "api_key:" + apiKey, nil
		}
		if userID != "" {
			return "user_id:" + userID, nil
		}
		if clientID != "" {
			return "client_id:" + clientID, nil
		}
		mode = "ip"
	}
	var value string
	switch mode {
	case "api_key":
		value = apiKey
	case "user_id":
		value = userID
	case "ip":
		ip := clientIP(r, trustedProxies)
		if ip != nil {
			value = ip.String()
		}
	default:
		return "", errors.New("unsupported identity mode")
	}
	if value == "" {
		return "", errors.New("required client identity is missing")
	}
	if len(value) > 256 {
		return "", errors.New("client identity exceeds 256 bytes")
	}
	return mode + ":" + value, nil
}

func clientIP(r *http.Request, trustedProxies []*net.IPNet) net.IP {
	peer := parseRemoteIP(r.RemoteAddr)
	if peer == nil || !isTrustedProxy(peer, trustedProxies) {
		return peer
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	chain := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		ip := net.ParseIP(strings.TrimSpace(part))
		if ip == nil {
			return peer
		}
		chain = append(chain, ip)
	}
	if len(chain) == 0 {
		return peer
	}
	current := peer
	for i := len(chain) - 1; i >= 0; i-- {
		if !isTrustedProxy(current, trustedProxies) {
			return current
		}
		current = chain[i]
	}
	return current
}

func parseRemoteIP(remoteAddr string) net.IP {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(strings.Trim(remoteAddr, "[]"))
}

func isTrustedProxy(ip net.IP, trusted []*net.IPNet) bool {
	for _, network := range trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseTrustedProxies(value string) ([]*net.IPNet, error) {
	var result []*net.IPNet
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return nil, errors.New("TRUSTED_PROXIES must contain valid comma-separated CIDRs")
		}
		result = append(result, network)
	}
	return result, nil
}

func resetTime(after time.Duration) string {
	return time.Now().Add(after).UTC().Format(time.RFC3339Nano)
}

func resetSeconds(after time.Duration) int64 {
	if after <= 0 {
		return 0
	}
	return int64(math.Ceil(after.Seconds()))
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
