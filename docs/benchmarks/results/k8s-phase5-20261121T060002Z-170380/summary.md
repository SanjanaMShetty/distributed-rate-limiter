# Phase 5 in-cluster load run

- Run ID: 20261121T060002Z-170380
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 1

Requests      [total, rate, throughput]         30000, 500.01, 500.01
Duration      [total, attack, wait]             59.999s, 59.998s, 362.715µs
Latencies     [min, mean, 50, 90, 95, 99, max]  233.435µs, 1.29ms, 807.901µs, 1.502ms, 1.97ms, 14.634ms, 83.842ms
Bytes In      [total, mean]                     3606977, 120.23
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
