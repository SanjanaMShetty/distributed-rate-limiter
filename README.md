# Distributed Rate Limiter

A learning project for building and measuring a low-latency, horizontally scalable rate limiter. The first milestone is a Go HTTP gateway with an in-process token-bucket middleware and Redis-backed shared state. Multiple gateway replicas use the same Redis state, so a caller has one quota across replicas.

## Current design

```text
HTTP clients
    |
    v
Go gateway replicas (token-bucket middleware, Prometheus metrics)
    |
    | one atomic Lua check-and-update per request
    v
Redis (single node locally; Redis Cluster in the sharding milestone)
    |
    +--> Prometheus --> Grafana
```

The request path stays in the gateway process and makes one Redis round trip. We are not putting a separate gRPC decision service in the MVP hot path: that would add another network hop. A gRPC integration can be added later as a comparison, with its added latency measured against the middleware design.

Redis is the quota state store. PostgreSQL is not part of the MVP; it may be considered later for durable rule management or audit history if the project gains those requirements. Prometheus stores time-series metrics separately from quota state.

## Phase 1: shared quota across gateway replicas

- Reusable `internal/engine` package resolves a path to a rule and makes the Redis-backed decision; the HTTP gateway only extracts identity and adapts the response.
- Two gateway containers on ports 8080 and 8081 share one Redis instance. The `/v1/phase1-demo` rule has capacity five for a repeatable cross-replica demo.
- The Redis Lua script atomically refills, decides, writes state, and sets a TTL. Tests check the key TTL and concurrent calls through two engines.
- Rules load at startup from `deploy/compose/rules.json`. Identities include API key, user ID, and IP. `X-Forwarded-For` is used only when `TRUSTED_PROXIES` contains the connecting proxy's CIDR.
- Redis checks have a strict 10 ms default timeout. If Redis is unavailable or too slow, Phase 1 fails open: it allows the request, marks it with `RateLimit-Degraded: fail-open`, and increments `rate_limiter_fail_open_total`. `/readyz` stays available and reports degraded Redis status. Phase 1 uses allow-through; bounded per-gateway local counters are deferred to the resilience phase.
- A limit breach fails fast with HTTP 429, `Retry-After`, and `RateLimit-*` headers. Requests are never queued in an application-side buffer. The gateway can start while Redis is unavailable.
- `/healthz`, `/readyz`, `/metrics`, graceful shutdown, Prometheus scraping of both gateways, and Grafana dashboards.

Run unit and HTTP adapter tests with `go test ./...`. The real-Redis integration test is opt-in: start Compose, then run `make integration` (or set `REDIS_INTEGRATION_ADDR`).

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

1. Local single-Redis token-bucket middleware and dashboard.
2. Concurrency correctness and repeatable benchmark harness.
3. Multiple algorithms and algorithm accuracy comparisons.
4. Redis Cluster sharding, failover experiments, and measured hot-key mitigation.
5. Kubernetes deployment, autoscaling, fault injection, and operational runbooks.
6. Optional Envoy/gRPC integration comparison.
