# Phase 5 in-cluster load run

- Run ID: 20261121T060457Z-172280
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 2

Requests      [total, rate, throughput]         30000, 500.02, 500.01
Duration      [total, attack, wait]             59.999s, 59.998s, 887.515µs
Latencies     [min, mean, 50, 90, 95, 99, max]  271.793µs, 4.946ms, 1.295ms, 9.786ms, 21.3ms, 59.228ms, 486.297ms
Bytes In      [total, mean]                     3571395, 119.05
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
