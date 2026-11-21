package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/redis/go-redis/v9"
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

func NewMetrics(redisClients ...redis.UniversalClient) *Metrics {
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
	if len(redisClients) > 0 && redisClients[0] != nil {
		registry.MustRegister(newRedisPoolCollector(redisClients[0]))
	}
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

type redisPoolCollector struct {
	client redis.UniversalClient
	descs  []*prometheus.Desc
}

func newRedisPoolCollector(client redis.UniversalClient) *redisPoolCollector {
	return &redisPoolCollector{
		client: client,
		descs: []*prometheus.Desc{
			prometheus.NewDesc("rate_limiter_redis_pool_connections", "Current Redis pool connections.", nil, nil),
			prometheus.NewDesc("rate_limiter_redis_pool_idle_connections", "Current idle Redis pool connections.", nil, nil),
			prometheus.NewDesc("rate_limiter_redis_pool_pending_requests", "Requests currently waiting for a Redis pool connection.", nil, nil),
			prometheus.NewDesc("rate_limiter_redis_pool_hits_total", "Redis pool connection reuse hits.", nil, nil),
			prometheus.NewDesc("rate_limiter_redis_pool_misses_total", "Redis pool connection misses that required a new connection.", nil, nil),
			prometheus.NewDesc("rate_limiter_redis_pool_timeouts_total", "Redis pool connection wait timeouts.", nil, nil),
			prometheus.NewDesc("rate_limiter_redis_pool_wait_duration_seconds_total", "Cumulative time spent waiting for Redis pool connections, in seconds.", nil, nil),
		},
	}
}

func (c *redisPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descs {
		ch <- desc
	}
}

func (c *redisPoolCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.client.PoolStats()
	values := []float64{
		float64(stats.TotalConns), float64(stats.IdleConns), float64(stats.PendingRequests),
		float64(stats.Hits), float64(stats.Misses), float64(stats.Timeouts),
		float64(stats.WaitDurationNs) / float64(time.Second),
	}
	for i, value := range values {
		metricType := prometheus.GaugeValue
		if i >= 3 {
			metricType = prometheus.CounterValue
		}
		ch <- prometheus.MustNewConstMetric(c.descs[i], metricType, value)
	}
}
