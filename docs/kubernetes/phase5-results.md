# Phase 5 Kubernetes measurements

These results came from the local kind cluster on WSL2/Docker Desktop, with Kubernetes v1.37.0, one control-plane node and two workers. They are observations from this environment, not production capacity guarantees.

## Steady load: fixed 500 requests/second

Three runs were recorded at one gateway replica, four at two replicas, and three at four replicas. Each run offered 500 requests/second for 60 seconds. Every included run completed 30,000 requests with 100% HTTP 200 and about 500 responses/second. The table reports median latency across runs and the observed run-to-run range.

| Gateway replicas | Runs | Throughput median | p50 median (range) | p95 median (range) | p99 median (range) | Max median (range) |
|---:|---:|---:|---:|---:|---:|---:|
| 1 | 3 | 500.01 req/s (499.97–500.01) | 0.679 ms (0.668–0.808) | 1.970 ms (1.304–2.090) | 14.634 ms (2.970–17.143) | 77.856 ms (20.705–83.842) |
| 2 | 4 | 500.01 req/s (500.00–500.01) | 1.150 ms (0.587–1.295) | 7.158 ms (1.664–21.300) | 39.378 ms (8.476–59.228) | 318.516 ms (75.893–550.322) |
| 4 | 3 | 500.01 req/s | 0.962 ms (0.954–1.049) | 2.180 ms (2.160–2.612) | 12.356 ms (11.711–15.932) | 59.298 ms (30.322–199.454) |

These fixed-rate runs show that each tested replica count sustained the offered 500 requests/second, but they do not show the maximum capacity of each count because the generator rate was held constant. Tail latency varied substantially—especially at two replicas—so don’t claim that adding replicas improved latency from these samples alone. The WSL2 host was also running other workloads during testing.

Representative run summaries: [one replica, run 1](../benchmarks/results/k8s-phase5-20261121T055852Z-169909/summary.md), [one replica, run 2](../benchmarks/results/k8s-phase5-20261121T060002Z-170380/summary.md), [one replica, run 3](../benchmarks/results/k8s-phase5-20261121T060113Z-170839/summary.md); [two replicas, run 1](../benchmarks/results/k8s-phase5-20261121T055145Z-169282/summary.md), [two replicas, run 2](../benchmarks/results/k8s-phase5-20261121T060227Z-171332/summary.md), [two replicas, run 3](../benchmarks/results/k8s-phase5-20261121T060342Z-171800/summary.md), [two replicas, run 4](../benchmarks/results/k8s-phase5-20261121T060457Z-172280/summary.md); [four replicas, run 1](../benchmarks/results/k8s-phase5-20261121T060615Z-172919/summary.md), [four replicas, run 2](../benchmarks/results/k8s-phase5-20261121T060728Z-173394/summary.md), [four replicas, run 3](../benchmarks/results/k8s-phase5-20261121T060841Z-173846/summary.md).

The first load-generator run, [`20261121T054803Z-168336`](../benchmarks/results/k8s-phase5-20261121T054803Z-168336/summary.md), returned 30,000 HTTP 404s because it targeted a route that wasn’t configured. It is excluded from the performance results; the route was corrected before collecting the successful runs.

## Higher offered load and CPU HPA scale-up

The earlier fixed-replica run at four gateways offered 1,000 requests/second for 90 seconds and completed 90,000 requests at 1,000 responses/second with 100% HTTP 200. Latency was p50 1.094 ms, p95 35.090 ms, p99 75.299 ms, and maximum 383.894 ms. This confirms the setup sustained that offered rate for that run; it does not establish a maximum-throughput limit.

See [the 1,000 requests/second run](../benchmarks/results/k8s-phase5-20261121T125801Z-10168/summary.md).

The CPU HPA test began with two replicas and offered 1,000 requests/second for 90 seconds. At the end of the run, the load-generator summary reported eight ready gateway replicas, equal to the configured maximum. All 90,000 requests returned HTTP 200 at 1,000 responses/second. Latency was p50 0.657 ms, p95 1.566 ms, p99 12.503 ms, and maximum 75.592 ms. The HPA later returned to two replicas after load stopped, confirming both scale-up and scale-down. The exact time to scale down was not captured.

Run ID: `20261121T145602Z-35147`.

## Disruption tests

| Scenario | Run | Offered load | Outcome | Latency |
|---|---|---:|---|---|
| Rolling update with two gateway replicas | [`20261121T063338Z-177133`](../benchmarks/results/k8s-phase5-20261121T063338Z-177133/summary.md) | 500 req/s for 30s | 15,000 HTTP 200; 100% success | p50 1.554 ms, p95 15.769 ms, p99 49.842 ms, max 298.705 ms |
| Delete one gateway pod during load | [`20261121T063512Z-177683`](../benchmarks/results/k8s-phase5-20261121T063512Z-177683/summary.md) | 500 req/s for 30s | 15,000 HTTP 200; 100% success | p50 1.789 ms, p95 27.662 ms, p99 63.605 ms, max 188.933 ms |

Both runs maintained successful HTTP responses, while tail latency increased during disruption. The exact replacement-pod readiness time and shared-quota behavior during the disruption were not captured in these load summaries.

## Monitoring, autoscaling, and delivery

- Prometheus: two gateway targets were observed `up` in namespace `rate-limiter`.
- Grafana: screenshots showed the dashboards rendering, but the scrape-health panel had no data and some runtime panels included unrelated targets. The updated gateway selectors and Kubernetes replica panel still need to be applied and checked against the live dashboard.
- CPU HPA: the load run reached eight ready replicas from a starting count of two at 1,000 req/s, then returned to two replicas after load stopped. Scale-up and scale-down are demonstrated; the exact scale-down duration was not captured.
- Trivy: awaiting a GitHub Actions run after the workflow is pushed. Record the workflow result, findings by severity, and remediation.
- Argo CD: awaiting installation and sync from the pushed Git branch. Record the deployed image tag, sync/health status, and self-heal drift test.

## Remaining evidence before calling Phase 5 complete

1. Apply and verify the updated Grafana dashboard in the Kubernetes monitoring namespace.
2. Push the project changes so GitHub Actions can run Trivy and publish the scanned immutable image.
3. Point Argo CD at the published image tag, verify sync/health, and demonstrate that it removes a temporary live-cluster change.
