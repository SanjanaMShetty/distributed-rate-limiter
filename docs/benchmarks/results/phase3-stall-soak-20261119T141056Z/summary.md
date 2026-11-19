# Redis stalled-response soak

- Run ID (UTC): 20261119T141056Z
- Offered load: 100/s for 60s
- Redis timeout: 10 ms
- Toxiproxy: downstream timeout toxic, timeout=0 (responses dropped until toxic removal)
- Route: /v1/local-fallback-demo
- Host kernel: Linux 6.18.58.1-microsoft-standard-WSL2 x86_64 GNU/Linux
- Logical CPUs: 18
- Go: go version go1.27.4 linux/amd64
- Vegeta build: /home/sanjana/go/bin/vegeta: go1.27.4;	path	github.com/tsenart/vegeta/v12;	mod	github.com/tsenart/vegeta/v12	v12.13.0	h1:J/UiNS3f69MkL0tsRLVUUV8uXXQZxdRUchtS+GYiSFc=;	dep	github.com/beorn7/perks	v1.0.1	h1:VlbKKnNfV8bJzeqoa4cOKqO6bYr3WgKZxO8Z16+hsOM=;	dep	github.com/c2h5oh/datasize	v0.0.0-20231215233829-aa82cc1e6500	h1:6lhrsTEnloDPXyeZBvSYvQf8u86jbKehZPVDDlkgDl4=;

## Vegeta report

Requests      [total, rate, throughput]         6000, 100.02, 2.07
Duration      [total, attack, wait]             59.991s, 59.99s, 548.356µs
Latencies     [min, mean, 50, 90, 95, 99, max]  341.04µs, 802.328µs, 666.358µs, 825.683µs, 929.091µs, 8.368ms, 13.73ms
Bytes In      [total, mean]                     1043625, 173.94
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           2.07%
Status Codes  [code:count]                      200:124  429:5876  
Error Set:
429 Too Many Requests

## Gateway A resilience counters during stall

# HELP rate_limiter_circuit_open_total Number of checks served without contacting Redis because the circuit breaker was open.
# TYPE rate_limiter_circuit_open_total counter
rate_limiter_circuit_open_total 5936
# HELP rate_limiter_degraded_decisions_total Rate-limit decisions made during Redis degradation, grouped by outage policy and result.
# TYPE rate_limiter_degraded_decisions_total counter
rate_limiter_degraded_decisions_total{decision="allow",policy="local"} 124
rate_limiter_degraded_decisions_total{decision="deny",policy="local"} 5876
# HELP rate_limiter_redis_inflight_rejected_total Number of checks diverted from Redis because the in-flight cap was reached.
# TYPE rate_limiter_redis_inflight_rejected_total counter
rate_limiter_redis_inflight_rejected_total 0
# HELP rate_limiter_store_errors_total Number of errors while checking or updating quota state in Redis.
# TYPE rate_limiter_store_errors_total counter
rate_limiter_store_errors_total 64
