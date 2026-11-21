# Phase 5 in-cluster load run

- Run ID: 20261121T055852Z-169909
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 1

Requests      [total, rate, throughput]         30000, 500.02, 500.01
Duration      [total, attack, wait]             59.999s, 59.998s, 1.314ms
Latencies     [min, mean, 50, 90, 95, 99, max]  224.928µs, 820.295µs, 678.894µs, 1.127ms, 1.304ms, 2.97ms, 20.705ms
Bytes In      [total, mean]                     3615390, 120.51
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
