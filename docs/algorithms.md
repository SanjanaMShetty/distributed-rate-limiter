# Rate-limit algorithm comparison

Phase 2 implements all algorithms in `internal/engine`; the gateway selects one per rule. The first three columns describe policy behavior, not measured performance. The performance comparison belongs to Phase 4 and must be recorded with the same machine, Redis, concurrency, and request profile.

| Algorithm | Rule parameters | Burst behavior | Accuracy semantics | State per client and rule |
|---|---|---|---|---|
| Token bucket | `capacity`, `refill_per_second` | Allows an immediate burst up to capacity, then refills continuously | Exact for the token-bucket model; can allow a configured burst above the sustained rate | One Redis hash |
| Fixed window | `capacity`, `window` | Allows the full limit at either side of a window boundary | Exact within each aligned fixed window; can admit nearly twice the limit across adjacent boundaries | One Redis hash |
| Sliding-window counter | `capacity`, `window` | Smooths the previous window's count by elapsed-window weight | Approximation of a rolling window; may differ from an exact request log, including a boundary over-admit | One Redis hash |

Each decision is one atomic Lua script. The window algorithms keep their counters and indices in one hash key so each check remains a single-key Redis Cluster operation.

## Correctness fixtures

- Token bucket: deterministic timestamps are checked against a Go token-bucket reference model; tests assert the balance stays in range and denials do not consume tokens.
- Fixed window: deterministic timestamps are checked against an aligned-window count model; each window admits at most its configured capacity.
- Sliding-window counter: deterministic timestamps are compared with an exact rolling-window log oracle. The fixture places four requests at 950 ms in the prior one-second window, then checks at 1010 ms. The weighted counter may allow one request while the exact log still contains four, illustrating the approximation boundary.
- Concurrency: many independent engine instances contend for one client quota in Redis; only the configured number of requests may be allowed for the token-bucket case.

## Current correctness results

| Algorithm/check | Result |
|---|---|
| Token bucket reference sequence | Matches the Go model for the deterministic refill/consume sequence; balance remains in `[0, capacity]`. |
| Fixed window reference sequence | Matches the aligned-window counter model; the configured limit is not exceeded within a window. |
| Sliding counter versus log oracle | The documented boundary fixture records one over-admit versus the exact rolling-window log. This is expected approximation error, not a strict rolling-window guarantee. |
| Concurrent token bucket | 100 requests across eight independent engine/client instances allow exactly five when capacity is five; run repeatedly and under `-race`. |
| Performance | Not measured in this phase; see the Phase 4 benchmark plan. |

## Performance comparison status

No algorithm is declared faster yet. Phase 4 will record throughput and p50/p95/p99 decision latency for allow-heavy, mixed, deny-heavy, and hot-key workloads. Correctness and performance results should be reported separately; a faster approximation is not interchangeable with an exact policy.
