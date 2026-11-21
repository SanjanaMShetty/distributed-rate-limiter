# Phase 5 in-cluster load run

- Run ID: 20261121T060227Z-171332
- Namespace: rate-limiter
- Offered load: 500/s for 60s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 2

Requests      [total, rate, throughput]         30000, 500.02, 500.01
Duration      [total, attack, wait]             59.999s, 59.998s, 867.117µs
Latencies     [min, mean, 50, 90, 95, 99, max]  277.724µs, 2.371ms, 1.128ms, 2.426ms, 9.478ms, 28.085ms, 150.734ms
Bytes In      [total, mean]                     3580415, 119.35
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:30000  
Error Set:
