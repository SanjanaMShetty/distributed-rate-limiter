# Distributed Rate Limiter

A learning project for building and measuring a low-latency, horizontally scalable rate limiter. The Go HTTP gateway calls a reusable in-process engine that uses Redis-backed shared state. Multiple gateway replicas use the same Redis state, so a caller has one quota across replicas.

## Current design

```text
HTTP clients
    |
    v
Go gateway replicas (selected engine algorithm)
    |
    | one atomic Lua check-and-update per request
    v
Toxiproxy (Compose-only fault injection; no toxic in normal runs)
    |
    v
Redis (single node locally; Redis Cluster in the sharding milestone)

Prometheus --scrapes /metrics--> Go gateway replicas
Grafana ----------------------> Prometheus
```

The request path stays in the gateway process and makes one Redis round trip. We are not putting a separate gRPC decision service in the MVP hot path: that would add another network hop. A gRPC integration can be added later as a comparison, with its added latency measured against the middleware design.

Toxiproxy is included only in the local Compose environment to inject Redis delay and outages. The rate-limiter production topology being evaluated remains gateway-to-Redis; Toxiproxy is not a required production component.

Redis is the quota state store. PostgreSQL is not part of the MVP; it may be considered later for durable rule management or audit history if the project gains those requirements. Prometheus stores time-series metrics separately from quota state.

## Phase 1: shared quota across gateway replicas

- Reusable `internal/engine` package resolves a path to a rule and makes the Redis-backed decision; the HTTP gateway only extracts identity and adapts the response.
- Two gateway containers on ports 8080 and 8081 share one Redis instance. The `/v1/phase1-demo` rule has capacity five for a repeatable cross-replica demo.
- The Redis Lua script atomically refills, decides, writes state, and sets a TTL. Tests check the key TTL and concurrent calls through two engines.
- Rules load at startup from `deploy/compose/rules.json`. Identities include API key, user ID, and IP. `X-Forwarded-For` is used only when `TRUSTED_PROXIES` contains the connecting proxy's CIDR.
- Redis checks have a strict 10 ms default timeout. The default outage policy is `fail_open`; rules can also select `fail_closed` or `local`. Local fallback uses a bounded, per-gateway token bucket, so quotas may diverge between replicas while Redis is unavailable. This is a deliberate availability trade-off, not a globally consistent fallback.
- A limit breach fails fast with HTTP 429, `Retry-After`, and `RateLimit-*` headers. Requests are never queued in an application-side buffer. The gateway can start while Redis is unavailable.
- `/healthz`, `/readyz`, `/metrics`, graceful shutdown, Prometheus scraping of both gateways, and Grafana dashboards.

Run unit and HTTP adapter tests with `go test ./...`. The real-Redis integration test is opt-in: start Compose, then run `make integration` (or set `REDIS_INTEGRATION_ADDR`).

## Phase 2: correctness and algorithms

Rules select `token_bucket`, `fixed_window`, or `sliding_window_counter` through `algorithm` in `deploy/compose/rules.json`. For window algorithms, `capacity` is the maximum count in the configured `window`; for token bucket it remains the burst capacity and `refill_per_second` is the sustained rate. Each gateway exports `rate_limiter_config_version_info{version=...}` so Prometheus can reveal config drift between replicas.

The sliding-window counter is an approximation. The fixed-window and token-bucket tests check their own reference semantics; the sliding-window comparison records differences against an exact rolling-window log oracle rather than claiming the approximation never over-admits relative to that oracle. See `docs/algorithms.md` for the comparison notes and correctness results. Run `go test -race ./...` before completing this phase.

## Phase 3: outage policies

Each rule may set `failure_policy` to `fail_open`, `fail_closed`, or `local`. The default remains `fail_open`. `fail_closed` rejects a request with HTTP 503 when Redis cannot answer within the configured timeout. `local` uses an in-memory token bucket on that gateway replica, bounded to 10,000 active identities with least-recently-used eviction and a five-minute idle expiry. Local fallback limits are per replica and are not synchronized; restarting a gateway clears its local state. The local fallback currently supports token-bucket rules only.

The Compose rules include `/v1/local-fallback-demo` and `/v1/fail-closed-demo`. To exercise them, stop Redis after the gateways have started, then send repeated requests with the same identity. Restore Redis afterward. A per-gateway circuit breaker opens after five Redis errors and allows one recovery probe after one second. Redis checks are admitted without queueing up to a cap of 256 in flight; excess checks immediately use the rule's outage policy. Prometheus exports `rate_limiter_circuit_open_total` and `rate_limiter_redis_inflight_rejected_total`. Automated tests cover policy behavior, breaker state transitions, the in-flight cap, and HTTP status/header behavior. Controlled latency and five-minute stalled-response soak runs have been recorded; further higher-concurrency and multi-replica resilience runs remain useful follow-up work. See [`docs/failure-tests.md`](docs/failure-tests.md) for the Toxiproxy experiment instructions; its admin API is bound to localhost only.

The first two-replica outage experiment admitted five local requests per gateway (10 total against a configured capacity of five), returned 429 after each local bucket emptied, and resumed shared Redis quota after Redis recovered. Under a 25 ms Redis response delay, each gateway separately handled 500 offered requests at 100/s: 14 returned 200 and 486 returned the expected 429, with no 5xx. Aggregate p99 was 12.4–12.6 ms, dominated by the timeout transition; most responses were fast denials. A stalled-response test produced the same local quota behavior and recovered to shared Redis quota (remaining 9 then 8 across gateways). These are initial fault-injection results, not a healthy-state capacity benchmark; see [`docs/failure-tests.md`](docs/failure-tests.md) for conditions, results, and limitations.

Use `scripts/chaos/compare-failure-policies.sh` to run a repeatable same-profile comparison of fail-open, local fallback, and fail-closed under injected Redis latency. In the first run, fail-open returned 500/500 requests as 200, local fallback returned 14/500 as 200 and 486 as 429, and fail-closed returned 500 intentional 503 responses. The run saved raw Vegeta results, reports, and gateway metrics under `docs/benchmarks/results/`; see [`docs/failure-tests.md`](docs/failure-tests.md) for full latency results and limits.

Prometheus also scrapes Go runtime and process metrics for goroutine and memory growth. Use `scripts/chaos/redis-stall-soak.sh` to run a stalled-Redis soak and save resource samples, Vegeta output, and recovery checks. The recorded five-minute run did not show a steady resource-growth trend; the default soak profile does not saturate the 256-check in-flight cap.

## Run locally

Prerequisites: Go 1.27+, Docker Desktop with Compose.

```powershell
docker compose -f deploy/compose/docker-compose.yml up --build
```

Then open:

- Demo endpoint: `http://localhost:8080/v1/resource`
- Second gateway: `http://localhost:8081/v1/resource`
- Prometheus: `http://localhost:9090`
- Grafana: `http://localhost:3000` (local demo credentials: `admin` / `admin`)

Send repeated requests with the same `X-API-Key`, `X-User-ID`, or `X-Client-ID` to observe one quota. Change `deploy/compose/rules.json` to adjust the rule; rule changes take effect after restarting the gateway. A token bucket is not a fixed window: capacity sets the maximum burst and refill rate sets the sustained allowance. The checked-in values are demo settings, not production recommendations.

### Rate-limit response fields

- `RateLimit-Limit`: configured bucket capacity or window limit.
- `RateLimit-Remaining`: whole quota units available now (token bucket values are floored).
- `RateLimit-Reset`: **seconds from now**, rounded up, until a token bucket is full or the configured window boundary is reached. It is not a Unix timestamp.
- JSON `reset_at`: the corresponding absolute UTC timestamp with sub-second precision.
- `Retry-After`: on 429, whole seconds to wait until at least one token/window slot should be available; it rounds up. On an allowed response, `Retry-After: 0` means no wait is required.
- During fail-open, quota state is unknown, so `remaining` and `reset_at` are `null`. The `RateLimit-Degraded` header identifies the outage policy in effect.

These separate `RateLimit-*` fields use this project's documented response contract; they are not the current IETF Internet-Draft format. The active draft defines `RateLimit-Policy` and a `RateLimit` field whose `t` parameter is a relative effective-window value. See the [current IETF draft](https://datatracker.ietf.org/doc/draft-ietf-httpapi-ratelimit-headers/) before claiming standards compatibility.

To prove the shared five-request quota, send the first five requests to port 8080 and the next request to port 8081, using the same unique `X-API-Key`, against `/v1/phase1-demo`. The sixth should return 429. To exercise the real Redis integration test, run `make integration` while the Compose Redis service is running.

## Measurement plan

No performance number is a target until a repeatable benchmark produces it. Record:

- Checks per second and p50/p95/p99 decision latency.
- Added latency against a no-op gateway baseline.
- Allowed, denied, and error rates for allowed-heavy, mixed, and deny-heavy traffic.
- Over-admits and under-admits under concurrency across multiple gateway replicas.
- Throughput and tail latency as gateway replicas and Redis shards increase.
- CPU and memory per process, Redis operations per second, and hot-key impact.
- Redis outage/failover error window, degraded decisions, and quota updates lost during failover.

Each benchmark record must include hardware, versions, rule settings, request profile, concurrency, warm-up, run duration, and raw results. See `docs/benchmarks/plan.md`.

## Planned milestones

1. Phase 0: setup, test workflow, and CI.
2. Phase 1: shared quota across two gateways with the embedded engine.
3. Phase 2: correctness models, algorithm selection, and configuration-drift visibility.
4. Phase 3: Redis timeout/failure policies, circuit breaker, local fallback experiments, and chaos testing.
5. Phase 4: repeatable performance benchmarks, Prometheus alerting, and recorded results.
6. Phase 5: Kubernetes/Helm, GitOps, scaling tests, and operational runbooks.
7. Phase 6: compare embedded, gRPC, and HTTP service boundaries, then publish the demo/write-up.

Phase 4 benchmark profiles and instructions are in [`docs/benchmarks/phase4.md`](docs/benchmarks/phase4.md); initial local measurements are summarized in [`docs/benchmarks/phase4-results.md`](docs/benchmarks/phase4-results.md). They are a Compose baseline, not production capacity guarantees. AWS validation is outside this project's planned scope.
