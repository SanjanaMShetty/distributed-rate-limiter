# Phase 4 benchmark results

These are observed local Compose results, not production capacity guarantees. Requests were sent to gateway A on WSL2 with no Toxiproxy toxic active. Gateways and Redis ran in Docker Compose; the request path included the local Toxiproxy hop. The measurements are useful as a reproducible baseline for later Kubernetes comparisons.

## Environment

| Item | Value |
|---|---|
| Host | WSL2, Linux 6.18.58.1-microsoft-standard-WSL2 x86_64 |
| Logical CPUs | 18 |
| Go | go1.27.4 linux/amd64 |
| Vegeta | v12.13.0 |
| Redis | 7.4.12 |
| Gateway | `http://localhost:8080` (gateway A) |
| Git revision in benchmark artifacts | `91222abc438b96bf1e008e88a71bae7d69cf7197` |
| Run duration | 30 seconds per run |

The benchmark script saves raw Vegeta data and metadata with each run under `docs/benchmarks/results/`. It also snapshots gateway metrics and Redis `INFO stats`/`INFO memory` before and after each run. The values below are summarized from the reports shared during this phase; use the saved artifacts as the detailed record.

## Repeated profiles at 100 offered requests/second

Each profile was run three times for 30 seconds (3,000 requests per run). Latencies are the median of the three runs. Throughput means successful 2xx responses per second in Vegeta's report; for allow-heavy traffic it matches the offered rate, while for quota-limited profiles it is lower by design.

| Profile | Response counts per run | 2xx ratio | 2xx throughput | Median p50 | Median p95 | Median p99 | Max observed |
|---|---:|---:|---:|---:|---:|---:|---:|
| `allow-hot` | 3,000 x 200 | 100% | 100.03/s | 1.218 ms | 1.524 ms | 1.987 ms | 5.42 ms |
| `allow-many` | 3,000 x 200 | 100% | 100.03/s | 1.219 ms | 1.499 ms | 1.91 ms | 6.555 ms |
| `mixed` | 1,599 x 200; 1,401 x 429 | 53.30% | 53.32/s | 1.224 ms | 1.539 ms | 1.884 ms | 5.268 ms |
| `deny-heavy` | 61 x 200; 2,939 x 429 | 2.03% | 2.03/s | 1.223 ms | 1.785 ms | 2.311 ms | 9.53 ms |

For `mixed`, the configured bucket (capacity 100, refill 50/s) allowed about half of the 100/s offered traffic after its initial burst. For `deny-heavy`, the 2/s token refill limited successful responses; its low 2xx throughput describes policy decisions, not gateway capacity. Aggregate percentiles include both 200 and 429 responses.

The `allow-many` target file contained 10,000 distinct API keys, but a 3,000-request run did not necessarily use all of them. At this profile the target stream cycles through the list; the realized identity count is bounded by the requests sent.

## Single-run `allow-hot` rate sweep

These are exploratory single samples, not repeated capacity measurements. All returned 200 and Vegeta throughput matched offered load.

| Offered rate | Requests | p95 | p99 | Max | Status |
|---:|---:|---:|---:|---:|---|
| 100/s | 3,000 | 1.492 ms | 1.853 ms | 5.42 ms | 100% HTTP 200 |
| 250/s | 7,500 | 1.15 ms | 1.607 ms | 4.296 ms | 100% HTTP 200 |
| 500/s | 15,000 | 1.254 ms | 1.789 ms | 5.954 ms | 100% HTTP 200 |
| 1,000/s | 30,000 | 1.085 ms | 1.357 ms | 8.119 ms | 100% HTTP 200 |

The 100/s row above is one of the three repeated `allow-hot` runs; its p95/p99 differ from the repeated-run medians. Latency did not rise consistently through 1,000/s, and the generator kept up. This sweep did not find the throughput ceiling. It does not justify a claim that the service supports only or exactly 1,000 requests/second.

## Repeated no-op baseline comparison at 100/s

Three alternating pairs were run at 100/s for 30 seconds, with all 3,000 requests returning HTTP 200 in each run. The table reports the median latency across the three runs. The gateway's observed p95 increase over the no-op HTTP responder was about 0.692 ms in this sample.

| Target | Median p50 | Median p95 | Median p99 | Max observed across runs |
|---|---:|---:|---:|---:|
| No-op HTTP baseline | 0.647 ms | 0.898 ms | 1.165 ms | 3.411 ms |
| Rate-limiter gateway | 1.228 ms | 1.590 ms | 2.005 ms | 15.701 ms |

This estimates local added request-path latency for the tested Compose setup. It is not a production overhead guarantee. The high gateway maximums are individual outliers; percentiles across three short runs are more representative than the single maximum.

## Algorithm allow-path comparison at 500/s

Three 30-second runs per algorithm completed all 15,000 offered requests with HTTP 200. Each profile used one identity and a high quota so the test compared the allow path without quota denials. Median latency across the three runs:

| Algorithm | Median p50 | Median p95 | Median p99 | Max observed across runs |
|---|---:|---:|---:|---:|
| Token bucket | 0.878 ms | 1.300 ms | 1.651 ms | 14.445 ms |
| Fixed window | 0.894 ms | 1.421 ms | 1.739 ms | 52.732 ms |
| Sliding-window counter | 0.795 ms | 1.269 ms | 1.720 ms | 14.945 ms |

The three algorithms have overlapping results at this load, so these samples do not identify a clear performance winner. Their policies differ; select an algorithm for its quota semantics, then use these numbers only as a local allow-path cost comparison. The large one-run maxima reinforce the need to compare distributions and repeat under controlled conditions.

## Redis CPU and connection-pool observations

Redis `INFO CPU` before/after snapshots showed median Redis process CPU-time increases of about 0.614 seconds during each 30-second `allow-hot` run at 100/s. At 500/s, the medians were approximately 1.757 seconds for token bucket, 1.869 seconds for fixed window, and 1.678 seconds for sliding-window counter. These are local container measurements and can include small background activity between snapshots.

Across the shared 100/s and 500/s benchmark snapshots, Redis pool pending requests and pool timeouts remained zero. The sampled connection count rose from 1 to as many as 6; snapshots were idle at the end of runs. These before/after snapshots do not capture peak in-run pool occupancy; use the Grafana time series for that view.

## Interpretation and remaining Phase 4 work

- At 100/s, the local deployment completed all offered requests in allow-heavy tests, and the mixed/deny-heavy responses matched their configured token-bucket behavior.
- `allow-hot` and `allow-many` had similar latency at this load. Higher rates, larger realized key sets, and repeated runs are needed before drawing a performance comparison.
- The rate sweep should continue after the Kubernetes phase if desired; these local results remain the Compose baseline.
- The repeated baseline and algorithm runs, Redis CPU snapshots, and pool snapshots are now recorded above. The dashboard and Prometheus rule loading have been verified, but one alert still needs to be observed firing during a controlled gateway outage and clearing after recovery.
- Keep generated `.bin`, target lists, and per-run snapshots as local/raw artifacts unless a selected compact result is useful for review. Commit this curated summary and a small number of representative reports/plots rather than every generated file.
