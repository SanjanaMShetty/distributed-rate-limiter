# Phase 5 in-cluster load run

- Run ID: 20261121T060841Z-173846
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 4

Requests      [total, rate, throughput]         30000, 500.02, 500.01
Duration      [total, attack, wait]             59.999s, 59.998s, 734.412µs
Latencies     [min, mean, 50, 90, 95, 99, max]  351.912µs, 1.712ms, 1.049ms, 2.082ms, 2.612ms, 15.932ms, 199.454ms
Bytes In      [total, mean]                     3581811, 119.39
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
