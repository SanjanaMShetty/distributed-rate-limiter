# Phase 5 in-cluster load run

- Run ID: 20261121T145602Z-35147
- Namespace: rate-limiter
- Offered load: 1000/s for 90s
- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow
- Gateway replicas: 8

Requests      [total, rate, throughput]         90000, 1000.01, 1000.00
Duration      [total, attack, wait]             1m30s, 1m30s, 719.014µs
Latencies     [min, mean, 50, 90, 95, 99, max]  169.519µs, 1.022ms, 657.49µs, 1.19ms, 1.566ms, 12.503ms, 75.592ms
Bytes In      [total, mean]                     10741270, 119.35
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:90000  
Error Set:
