# Phase 5 in-cluster load run

- Run ID: 20261121T060342Z-171800
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 2

Requests      [total, rate, throughput]         30000, 500.01, 500.00
Duration      [total, attack, wait]             1m0s, 59.998s, 2.068ms
Latencies     [min, mean, 50, 90, 95, 99, max]  273.526µs, 2.984ms, 1.172ms, 2.254ms, 4.838ms, 50.671ms, 550.322ms
Bytes In      [total, mean]                     3555213, 118.51
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
