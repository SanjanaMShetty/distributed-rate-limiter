# Phase 3 outage policy comparison

- Run ID (UTC): 20261119T140113Z
- Offered load: 100 requests/second for 5 seconds per policy
- Redis timeout: 10 ms
- Toxiproxy: 25 ms downstream latency
- Target: gateway A on localhost:8080
- Host kernel: Linux 6.18.58.1-microsoft-standard-WSL2 x86_64 GNU/Linux
- Logical CPUs: 18
- Go: go version go1.27.4 linux/amd64
- Vegeta: Version: 

## fail_open

Route: /v1/resource; API key: phase3-fail_open-20261119T140113Z

Requests      [total, rate, throughput]         500, 100.19, 100.17
Duration      [total, attack, wait]             4.991s, 4.991s, 692.256µs
Latencies     [min, mean, 50, 90, 95, 99, max]  341.037µs, 892.594µs, 645.53µs, 802.802µs, 854.13µs, 12.248ms, 14.187ms
Bytes In      [total, mean]                     52500, 105.00
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:500  
Error Set:

Gateway A counters after fail_open:
# HELP rate_limiter_circuit_open_total Number of checks served without contacting Redis because the circuit breaker was open.
# TYPE rate_limiter_circuit_open_total counter
rate_limiter_circuit_open_total 490
# HELP rate_limiter_degraded_decisions_total Rate-limit decisions made during Redis degradation, grouped by outage policy and result.
# TYPE rate_limiter_degraded_decisions_total counter
rate_limiter_degraded_decisions_total{decision="allow",policy="fail_open"} 500
# HELP rate_limiter_fail_open_total Number of requests allowed because Redis was unavailable or timed out.
# TYPE rate_limiter_fail_open_total counter
rate_limiter_fail_open_total 500
# HELP rate_limiter_redis_inflight_rejected_total Number of checks diverted from Redis because the in-flight cap was reached.
# TYPE rate_limiter_redis_inflight_rejected_total counter
rate_limiter_redis_inflight_rejected_total 0
# HELP rate_limiter_store_errors_total Number of errors while checking or updating quota state in Redis.
# TYPE rate_limiter_store_errors_total counter
rate_limiter_store_errors_total 10

## local

Route: /v1/local-fallback-demo; API key: phase3-local-20261119T140113Z

Requests      [total, rate, throughput]         500, 100.21, 2.81
Duration      [total, attack, wait]             4.99s, 4.99s, 747.246µs
Latencies     [min, mean, 50, 90, 95, 99, max]  384.24µs, 907.823µs, 665.577µs, 855.213µs, 996.478µs, 12.176ms, 13.269ms
Bytes In      [total, mean]                     86794, 173.59
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           2.80%
Status Codes  [code:count]                      200:14  429:486  
Error Set:
429 Too Many Requests

Gateway A counters after local:
# HELP rate_limiter_circuit_open_total Number of checks served without contacting Redis because the circuit breaker was open.
# TYPE rate_limiter_circuit_open_total counter
rate_limiter_circuit_open_total 490
# HELP rate_limiter_degraded_decisions_total Rate-limit decisions made during Redis degradation, grouped by outage policy and result.
# TYPE rate_limiter_degraded_decisions_total counter
rate_limiter_degraded_decisions_total{decision="allow",policy="local"} 14
rate_limiter_degraded_decisions_total{decision="deny",policy="local"} 486
# HELP rate_limiter_fail_open_total Number of requests allowed because Redis was unavailable or timed out.
# TYPE rate_limiter_fail_open_total counter
rate_limiter_fail_open_total 0
# HELP rate_limiter_redis_inflight_rejected_total Number of checks diverted from Redis because the in-flight cap was reached.
# TYPE rate_limiter_redis_inflight_rejected_total counter
rate_limiter_redis_inflight_rejected_total 0
# HELP rate_limiter_store_errors_total Number of errors while checking or updating quota state in Redis.
# TYPE rate_limiter_store_errors_total counter
rate_limiter_store_errors_total 10

## fail_closed

Route: /v1/fail-closed-demo; API key: phase3-fail_closed-20261119T140113Z

Requests      [total, rate, throughput]         500, 100.19, 0.00
Duration      [total, attack, wait]             4.991s, 4.99s, 680.369µs
Latencies     [min, mean, 50, 90, 95, 99, max]  373.26µs, 1.111ms, 815.721µs, 1.281ms, 1.51ms, 12.495ms, 13.631ms
Bytes In      [total, mean]                     50500, 101.00
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           0.00%
Status Codes  [code:count]                      503:500  
Error Set:
503 Service Unavailable

Gateway A counters after fail_closed:
# HELP rate_limiter_circuit_open_total Number of checks served without contacting Redis because the circuit breaker was open.
# TYPE rate_limiter_circuit_open_total counter
rate_limiter_circuit_open_total 490
# HELP rate_limiter_degraded_decisions_total Rate-limit decisions made during Redis degradation, grouped by outage policy and result.
# TYPE rate_limiter_degraded_decisions_total counter
rate_limiter_degraded_decisions_total{decision="deny",policy="fail_closed"} 500
# HELP rate_limiter_fail_open_total Number of requests allowed because Redis was unavailable or timed out.
# TYPE rate_limiter_fail_open_total counter
rate_limiter_fail_open_total 0
# HELP rate_limiter_redis_inflight_rejected_total Number of checks diverted from Redis because the in-flight cap was reached.
# TYPE rate_limiter_redis_inflight_rejected_total counter
rate_limiter_redis_inflight_rejected_total 0
# HELP rate_limiter_store_errors_total Number of errors while checking or updating quota state in Redis.
# TYPE rate_limiter_store_errors_total counter
rate_limiter_store_errors_total 10

