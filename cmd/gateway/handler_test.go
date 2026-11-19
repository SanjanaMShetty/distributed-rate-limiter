package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SanjanaMShetty/distributed-rate-limiter/internal/engine"
	"github.com/SanjanaMShetty/distributed-rate-limiter/internal/observability"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func makeTestHandler(t *testing.T, server *miniredis.Miniredis, capacity float64) http.Handler {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	t.Cleanup(func() { _ = client.Close() })
	rule := engine.Rule{Name: "test", Path: "/v1/test", Capacity: capacity, RefillPerSec: 1, KeyTTL: time.Minute, Identity: "auto"}
	metrics := observability.NewMetrics()
	limiter, err := engine.New(client, []engine.Rule{rule}, 100*time.Millisecond, metrics)
	if err != nil {
		t.Fatal(err)
	}
	return newHandler(limiter, client, metrics, nil)
}

func TestTwoGatewayHandlersShareQuotaAndReturnRateLimitResponse(t *testing.T) {
	server := miniredis.RunT(t)
	first := makeTestHandler(t, server, 5)
	second := makeTestHandler(t, server, 5)
	for i := 0; i < 6; i++ {
		handler := first
		if i%2 == 1 {
			handler = second
		}
		req := httptest.NewRequest("GET", "/v1/test", nil)
		req.Header.Set("X-API-Key", "shared-demo-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		want := 200
		if i == 5 {
			want = 429
		}
		if response.Code != want {
			t.Fatalf("request %d returned %d, want %d; body=%s", i+1, response.Code, want, response.Body.String())
		}
		if i == 4 && response.Header().Get("RateLimit-Remaining") != "0" {
			t.Fatalf("last allowed request remaining header = %q, want 0", response.Header().Get("RateLimit-Remaining"))
		}
		if i == 5 {
			for key, wantValue := range map[string]string{"RateLimit-Limit": "5", "RateLimit-Remaining": "0", "Retry-After": "1"} {
				if got := response.Header().Get(key); got != wantValue {
					t.Errorf("%s = %q, want %q", key, got, wantValue)
				}
			}
			if !strings.Contains(response.Body.String(), `"allowed":false`) {
				t.Errorf("denial body missing allowed:false: %s", response.Body.String())
			}
		}
	}
}

func TestGatewayFailsOpenOnRedisErrorWithinBudget(t *testing.T) {
	server := miniredis.RunT(t)
	handler := makeTestHandler(t, server, 5)
	server.SetError("ERR simulated outage")
	req := httptest.NewRequest("GET", "/v1/test", nil)
	req.Header.Set("X-API-Key", "during-outage")
	response := httptest.NewRecorder()
	started := time.Now()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("fail-open returned %d, want 200: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("RateLimit-Degraded") != "fail-open" {
		t.Fatalf("missing fail-open response header: %v", response.Header())
	}
	if !strings.Contains(response.Body.String(), `"degraded":true`) {
		t.Fatalf("body should mark degraded decision: %s", response.Body.String())
	}
	if time.Since(started) > time.Second {
		t.Fatalf("Redis outage blocked request for %s", time.Since(started))
	}
}

func TestGatewayReturns503ForFailClosedRedisOutage(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	defer client.Close()
	rule := engine.Rule{Name: "closed", Path: "/v1/test", Capacity: 2, RefillPerSec: 1, KeyTTL: time.Minute, Identity: "auto", FailurePolicy: engine.FailureClosed}
	metrics := observability.NewMetrics()
	limiter, err := engine.New(client, []engine.Rule{rule}, 100*time.Millisecond, metrics)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(limiter, client, metrics, nil)
	server.SetError("ERR simulated outage")
	req := httptest.NewRequest("GET", "/v1/test", nil)
	req.Header.Set("X-API-Key", "outage-client")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("RateLimit-Degraded") != engine.FailureClosed {
		t.Fatalf("expected degraded 503, got status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestGatewayUsesLocalQuotaDuringRedisOutage(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: 0})
	defer client.Close()
	rule := engine.Rule{Name: "local", Path: "/v1/test", Capacity: 2, RefillPerSec: 1, KeyTTL: time.Minute, Identity: "auto", FailurePolicy: engine.FailureLocal}
	metrics := observability.NewMetrics()
	limiter, err := engine.New(client, []engine.Rule{rule}, 100*time.Millisecond, metrics)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(limiter, client, metrics, nil)
	server.SetError("ERR simulated outage")
	for i, wantStatus := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests} {
		req := httptest.NewRequest("GET", "/v1/test", nil)
		req.Header.Set("X-API-Key", "outage-client")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != wantStatus || response.Header().Get("RateLimit-Degraded") != engine.FailureLocal {
			t.Fatalf("request %d: status=%d headers=%v body=%s", i+1, response.Code, response.Header(), response.Body.String())
		}
		if i == 0 && (response.Header().Get("RateLimit-Remaining") != "1" || !strings.Contains(response.Body.String(), `"remaining":1`) || !strings.Contains(response.Body.String(), `"reset_at":"`)) {
			t.Fatalf("local fallback response should report quota and reset details: headers=%v body=%s", response.Header(), response.Body.String())
		}
		if i == 0 {
			resetHeader, parseErr := strconv.ParseInt(response.Header().Get("RateLimit-Reset"), 10, 64)
			var body map[string]interface{}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			resetAt, timeErr := time.Parse(time.RFC3339Nano, body["reset_at"].(string))
			if parseErr != nil || timeErr != nil || resetHeader != 1 || !resetAt.After(time.Now()) {
				t.Fatalf("reset should be reported as a relative second count and future UTC time: header=%q body=%v parseErr=%v timeErr=%v", response.Header().Get("RateLimit-Reset"), body, parseErr, timeErr)
			}
		}
		if i == 2 && (response.Header().Get("RateLimit-Remaining") != "0" || response.Header().Get("Retry-After") == "") {
			t.Fatalf("local denial should report zero remaining and retry timing: headers=%v", response.Header())
		}
	}
}

func TestReadyEndpointStaysAvailableWhenRedisIsDown(t *testing.T) {
	server := miniredis.RunT(t)
	handler := makeTestHandler(t, server, 5)
	server.SetError("ERR simulated outage")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/readyz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "degraded_fail_open") {
		t.Fatalf("ready endpoint should stay available but report degraded Redis: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestIdentityUsesForwardedIPOnlyFromTrustedProxy(t *testing.T) {
	_, trustedNetwork, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	trusted := []*net.IPNet{trustedNetwork}

	untrustedRequest := httptest.NewRequest("GET", "http://gateway/v1/test", nil)
	untrustedRequest.RemoteAddr = "198.51.100.8:1234"
	untrustedRequest.Header.Set("X-Forwarded-For", "203.0.113.7")
	identity, err := resolveIdentity(untrustedRequest, "ip", trusted)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "ip:198.51.100.8" {
		t.Fatalf("untrusted forwarded identity = %q", identity)
	}

	trustedRequest := httptest.NewRequest("GET", "http://gateway/v1/test", nil)
	trustedRequest.RemoteAddr = "10.0.0.9:443"
	trustedRequest.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.4")
	identity, err = resolveIdentity(trustedRequest, "ip", trusted)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "ip:203.0.113.7" {
		t.Fatalf("trusted forwarded identity = %q, want original client IP", identity)
	}
}

func TestReadyAndLiveEndpoints(t *testing.T) {
	server := miniredis.RunT(t)
	handler := makeTestHandler(t, server, 5)
	for _, path := range []string{"/healthz", "/readyz"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != 200 {
			t.Errorf("%s returned %d: %s", path, recorder.Code, recorder.Body.String())
		}
	}
}
