# Benchmark plan

## Profiles

- Allowed-heavy: most requests are below quota.
- Mixed: callers cross the quota during the run.
- Deny-heavy: most requests are already over quota.
- Hot-key: many concurrent requests contend on one caller key.
- Many-key: requests spread across many caller keys.

## Measurements

Capture request/decision throughput, p50/p95/p99 end-to-end latency, limiter-added latency over a no-op baseline, allow/deny/error ratio, quota accuracy against a reference model, gateway CPU/memory, Redis CPU/memory/operations per second, and per-shard traffic when clustered.

For resilience runs, capture client-visible error rate, degraded-decision rate, recovery time, and quota state lost or over-admission around Redis restart/failover.

## Reproducibility record

Every report includes machine/CPU/memory, OS, Go version, Redis version/topology, container limits, commit SHA, rule parameters, generator and version, concurrency, request profile, warm-up, run duration, and raw result files. Repeat runs and report the median and spread; do not select only the best run.
