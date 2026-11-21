# Phase 5 in-cluster load run

- Run ID: 20261121T063338Z-177133
- Namespace: rate-limiter
- Offered load: 500/s for 30s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 2

Requests      [total, rate, throughput]         15000, 500.05, 500.00
Duration      [total, attack, wait]             30s, 29.997s, 3.16ms
Latencies     [min, mean, 50, 90, 95, 99, max]  330.314µs, 3.95ms, 1.554ms, 5.455ms, 15.769ms, 49.842ms, 298.705ms
Bytes In      [total, mean]                     1759227, 117.28
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:15000  
Error Set:
