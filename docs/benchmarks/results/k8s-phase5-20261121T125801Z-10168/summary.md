# Phase 5 in-cluster load run

- Run ID: 20261121T125801Z-10168
- Namespace: rate-limiter
- Offered load: 1000/s for 90s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 4

Requests      [total, rate, throughput]         90000, 1000.01, 1000.00
Duration      [total, attack, wait]             1m30s, 1m30s, 1.008ms
Latencies     [min, mean, 50, 90, 95, 99, max]  186.287µs, 6.486ms, 1.094ms, 21.992ms, 35.09ms, 75.299ms, 383.894ms
Bytes In      [total, mean]                     10594882, 117.72
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:90000  
Error Set:
