# Phase 5 in-cluster load run

- Run ID: 20261121T055145Z-169282
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 2

Requests      [total, rate, throughput]         30000, 500.02, 500.01
Duration      [total, attack, wait]             59.999s, 59.998s, 885.95µs
Latencies     [min, mean, 50, 90, 95, 99, max]  179.134µs, 868.756µs, 587.384µs, 1.221ms, 1.664ms, 8.476ms, 75.893ms
Bytes In      [total, mean]                     3641853, 121.40
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
