# Phase 5 in-cluster load run

- Run ID: 20261121T063512Z-177683
- Namespace: rate-limiter
- Offered load: 500/s for 30s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 2

Requests      [total, rate, throughput]         15000, 500.03, 500.02
Duration      [total, attack, wait]             29.999s, 29.998s, 938.735µs
Latencies     [min, mean, 50, 90, 95, 99, max]  352.388µs, 5.742ms, 1.789ms, 13.939ms, 27.662ms, 63.605ms, 188.933ms
Bytes In      [total, mean]                     1738212, 115.88
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:15000  
Error Set:
