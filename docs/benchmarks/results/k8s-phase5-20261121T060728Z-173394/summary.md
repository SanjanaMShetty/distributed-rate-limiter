# Phase 5 in-cluster load run

- Run ID: 20261121T060728Z-173394
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 4

Requests      [total, rate, throughput]         30000, 500.02, 500.01
Duration      [total, attack, wait]             59.999s, 59.998s, 678.242µs
Latencies     [min, mean, 50, 90, 95, 99, max]  339.397µs, 1.276ms, 962.003µs, 1.788ms, 2.18ms, 12.356ms, 30.322ms
Bytes In      [total, mean]                     3584455, 119.48
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
