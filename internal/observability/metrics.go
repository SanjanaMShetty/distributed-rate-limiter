package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type Metrics struct {
	registry            *prometheus.Registry
	decisions           *prometheus.CounterVec
	decisionDuration    prometheus.Histogram
	storeErrors         prometheus.Counter
	failOpenDecisions   prometheus.Counter
	circuitOpen         prometheus.Counter
	inFlightRejected    prometheus.Counter
	degradedDecisions   *prometheus.CounterVec
	configVersion       *prometheus.GaugeVec
	httpRequests        *prometheus.CounterVec
	httpRequestDuration prometheus.Histogram
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rate_limiter_decisions_total",
			Help: "Number of allow and deny decisions returned by the rate-limit engine.",
		}, []string{"outcome"}),
		decisionDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "rate_limiter_decision_duration_seconds",
			Help:    "Time spent performing a Redis-backed limiter decision.",
			Buckets: prometheus.DefBuckets,
		}),
		storeErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rate_limiter_store_errors_total",
			Help: "Number of errors while checking or updating quota state in Redis.",
		}),
		failOpenDecisions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rate_limiter_fail_open_total",
			Help: "Number of requests allowed because Redis was unavailable or timed out.",
		}),
		circuitOpen: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rate_limiter_circuit_open_total",
			Help: "Number of checks served without contacting Redis because the circuit breaker was open.",
		}),
		inFlightRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rate_limiter_redis_inflight_rejected_total",
			Help: "Number of checks diverted from Redis because the in-flight cap was reached.",
		}),
		degradedDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rate_limiter_degraded_decisions_total",
			Help: "Rate-limit decisions made during Redis degradation, grouped by outage policy and result.",
		}, []string{"policy", "decision"}),
		configVersion: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "rate_limiter_config_version_info",
			Help: "The active normalized rate-limit configuration version on this gateway replica.",
		}, []string{"version"}),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "HTTP requests to the protected demo endpoint, grouped by route and status.",
		}, []string{"route", "status"}),
		httpRequestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "gateway_http_request_duration_seconds",
			Help:    "End-to-end duration for the protected demo endpoint.",
			Buckets: prometheus.DefBuckets,
		}),
	}
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.decisions, m.decisionDuration, m.storeErrors, m.failOpenDecisions,
		m.circuitOpen, m.inFlightRejected, m.degradedDecisions, m.configVersion,
		m.httpRequests, m.httpRequestDuration,
	)
	return m
}

func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

func (m *Metrics) ObserveDecision(outcome string, elapsed time.Duration) {
	m.decisions.WithLabelValues(outcome).Inc()
	m.decisionDuration.Observe(elapsed.Seconds())
}

func (m *Metrics) ObserveStoreError() { m.storeErrors.Inc() }

func (m *Metrics) ObserveFailOpen() { m.failOpenDecisions.Inc() }

func (m *Metrics) ObserveCircuitOpen() { m.circuitOpen.Inc() }

func (m *Metrics) ObserveRedisInFlightRejected() { m.inFlightRejected.Inc() }

func (m *Metrics) ObserveDegradedPolicy(policy, decision string) {
	m.degradedDecisions.WithLabelValues(policy, decision).Inc()
}

func (m *Metrics) SetConfigVersion(version string) {
	m.configVersion.Reset()
	m.configVersion.WithLabelValues(version).Set(1)
}

func (m *Metrics) ObserveHTTP(route string, statusCode int, elapsed time.Duration) {
	m.httpRequests.WithLabelValues(route, statusLabel(statusCode)).Inc()
	m.httpRequestDuration.Observe(elapsed.Seconds())
}

func statusLabel(status int) string {
	switch status {
	case 200:
		return "200"
	case 401:
		return "401"
	case 429:
		return "429"
	case 503:
		return "503"
	case 500:
		return "500"
	default:
		return "other"
	}
}
