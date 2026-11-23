# Distributed Rate Limiter

A Go rate-limiting engine embedded in a small HTTP gateway. Gateway replicas use Redis to make atomic quota decisions, so a client shares one limit across replicas. The project explores correctness, latency, outage behavior, Kubernetes operations, and measured trade-offs.

> This is a measured systems project and local Kubernetes demo. Its benchmark results are specific to the recorded WSL2/Docker Desktop environment; they are not production capacity guarantees.

## How a request is limited

```text
Client → Go HTTP gateway → reusable Go engine → Redis Lua script
                                  └── Prometheus metrics → Grafana
```

The engine resolves a request identity and rule, then runs one Redis Lua script to atomically refill/count, decide, update state, and set key expiry. Multiple gateways therefore enforce a shared quota. The engine is a Go package (`internal/engine`); the gateway adapts HTTP identities, status codes, and headers around it.

There is no separate rate-limit service on the request path. This avoids an additional network hop. A remote gRPC adapter is a possible future comparison, not part of the current measured design.

## What it supports

- **Identity:** API key, user ID, or client IP. Forwarded IP headers are considered only for configured trusted proxies.
- **Algorithms:** token bucket, fixed window, and sliding-window counter, selected per rule.
- **Responses:** immediate `429 Too Many Requests` on limit breaches, with `Retry-After` and rate-limit headers. Excess requests are not queued.
- **Redis outage policies:** fail-open, fail-closed (`503`), or bounded per-gateway local token-bucket fallback. Local fallback keeps traffic available but quotas can diverge between gateways.
- **Operations:** health endpoints, graceful shutdown, Prometheus metrics and alerts, Grafana dashboard, Redis fault injection with Toxiproxy, Helm deployment, CPU-based HPA, and GitHub Actions checks/scans.

Redis is the shared quota-state store. PostgreSQL is not used: durable rule administration and audit history are outside the current scope.

## Run locally

Prerequisites: Go 1.27+, Docker Desktop, and Docker Compose.

```bash
docker compose -f deploy/compose/docker-compose.yml up --build
```

Then try the gateway and dashboards:

| Service | Address |
|---|---|
| Gateway replicas | `http://localhost:8080`, `http://localhost:8081` |
| Prometheus | `http://localhost:9090` |
| Grafana | `http://localhost:3000` (`admin` / `admin` for the local demo) |

Send repeated requests with the same identity header to either gateway. Rules are in [`deploy/compose/rules.json`](deploy/compose/rules.json); restart the gateway after editing them.

```bash
curl -i -H 'X-API-Key: demo-client' http://localhost:8080/v1/resource
curl -i -H 'X-API-Key: demo-client' http://localhost:8081/v1/resource
```

The demo rule is intentionally small so that allowed and denied responses are easy to observe. These values are for demonstration, not a general-purpose production policy.

## Verify and explore

```bash
go test ./...
go vet ./...
go build ./...
CGO_ENABLED=1 go test -race ./...
```

With Compose Redis running, run the opt-in real-Redis integration tests:

```bash
make integration
```

For the Kubernetes walkthrough, use [`docs/kubernetes/phase5.md`](docs/kubernetes/phase5.md). It covers the local Kind cluster, Helm chart, in-cluster load generator, and CPU HPA. The checked-in chart uses a single ephemeral Redis pod for local experiments; it is not a highly available Redis deployment.

## Measurements

The recorded results below are from WSL2 on an 18-logical-CPU host, Go 1.27.4, Redis 7.4.12, and Vegeta 12.13.0. They should be read with their test conditions and limitations in [`docs/benchmarks/phase4-results.md`](docs/benchmarks/phase4-results.md) and [`docs/kubernetes/phase5-results.md`](docs/kubernetes/phase5-results.md).

| Experiment | Observed result |
|---|---|
| Compose allow-heavy, one hot identity, 100 requests/s | 100% HTTP 200; median p99 1.99 ms across three 30-second runs |
| Compose baseline comparison, 100 requests/s | Gateway median p95 1.59 ms vs. no-op HTTP baseline 0.90 ms in the recorded runs |
| Compose rate sweep, one hot identity | 100% HTTP 200 at 1,000 offered requests/s in one 30-second run; this did not find the capacity ceiling |
| Kind load with CPU HPA | 1,000 requests/s for 90 seconds; 90,000 HTTP 200 responses; HPA scaled from 2 to 8 gateway replicas and later back to 2 |

These measurements demonstrate behavior at specific offered loads; they do not establish maximum throughput or prove that replica scaling improves latency. Kubernetes tail latency varied between runs. See the results documents for p50/p95/p99 ranges, disruption tests, and caveats.

## Repository map

```text
cmd/gateway/                 HTTP gateway
internal/engine/             reusable rate-limit engine and Redis scripts
internal/observability/      Prometheus metrics
deploy/compose/              local Redis, gateways, Prometheus, Grafana, Toxiproxy
deploy/k8s/helm/             Kubernetes Helm chart
deploy/k8s/argocd/           Argo CD Application manifest
scripts/                     benchmark, chaos, and Kubernetes workflows
docs/                        architecture, algorithm, failure, and benchmark notes
```

## Design notes and limitations

- Token bucket allows bursts up to capacity, then refills at the configured rate. Fixed window can burst at adjacent window boundaries. Sliding-window counter is an approximation and can differ from an exact rolling log.
- Redis provides the shared decision state, but a single Redis instance is a local-demo topology. Redis Cluster, managed failover, and their consistency/performance trade-offs have not been validated here.
- Fail-open intentionally allows unmetered traffic when Redis is unavailable. Local fallback limits are per gateway and may over-admit globally. Choose policy per rule based on the protected resource.
- CPU HPA scales gateway pods, not Redis. The measured HPA run is evidence for that local cluster and workload only.
- The in-cluster synthetic load generator uses HTTP on a private Kind ClusterIP service with generated identities. It must not be exposed to untrusted networks; production traffic should terminate TLS at the ingress boundary.

## Future improvements

- **Compare a separate gRPC decision service** with the current embedded engine. Keep the Redis setup and load profile the same, then compare p50/p95/p99 latency and throughput to quantify the cost of the extra network hop.
- **Evaluate demand-based autoscaling** against the current CPU HPA. Compare requests per second per pod with queue depth or in-flight requests; these signals may better reflect gateway pressure than total request rate. Include Redis saturation and tail latency so scaling gateways does not simply push overload to Redis. Use a custom or external metrics adapter if the chosen signal is not available through the standard resource metrics API.

## Further reading

- [Architecture decisions](docs/architecture.md)
- [Algorithm behavior and correctness](docs/algorithms.md)
- [Redis failure experiments](docs/failure-tests.md)
- [Benchmark plan and results](docs/benchmarks/plan.md) · [Phase 4 results](docs/benchmarks/phase4-results.md)
- [Kubernetes guide](docs/kubernetes/phase5.md) · [Phase 5 results](docs/kubernetes/phase5-results.md)
