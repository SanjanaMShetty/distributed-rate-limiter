# Phase 5 in-cluster load run

- Run ID: 20261121T054803Z-168336
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 2

Requests      [total, rate, throughput]         30000, 500.01, 0.00
Duration      [total, attack, wait]             59.999s, 59.998s, 664.266µs
Latencies     [min, mean, 50, 90, 95, 99, max]  186.401µs, 621.667µs, 535.253µs, 990.971µs, 1.148ms, 1.569ms, 15.959ms
Bytes In      [total, mean]                     570000, 19.00
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           0.00%
Status Codes  [code:count]                      404:30000  
Error Set:
404 Not Found
