# Phase 5 in-cluster load run

- Run ID: 20261121T060113Z-170839
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 1

Requests      [total, rate, throughput]         30000, 500.02, 499.97
Duration      [total, attack, wait]             1m0s, 59.998s, 6.143ms
Latencies     [min, mean, 50, 90, 95, 99, max]  236.167µs, 1.26ms, 667.642µs, 1.433ms, 2.09ms, 17.143ms, 77.856ms
Bytes In      [total, mean]                     3601215, 120.04
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
