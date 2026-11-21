# Phase 5 in-cluster load run

- Run ID: 20261121T060615Z-172919
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 4

Requests      [total, rate, throughput]         30000, 500.02, 500.01
Duration      [total, attack, wait]             59.999s, 59.998s, 774.744µs
Latencies     [min, mean, 50, 90, 95, 99, max]  317.745µs, 1.255ms, 953.678µs, 1.793ms, 2.16ms, 11.711ms, 59.298ms
Bytes In      [total, mean]                     3578812, 119.29
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
