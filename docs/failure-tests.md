# Failure behavior experiments

This page records observed behavior, not a performance benchmark. The local run used Docker Compose with two gateway replicas and one Redis container on 2026-11-19. Requests were sent sequentially from WSL. No latency or throughput claim is made from these functional checks.

## Redis outage: local fallback across two replicas

Configuration: `/v1/local-fallback-demo`, token bucket capacity 5 and refill rate 2 tokens/second. Redis was stopped while both gateways remained running. The same API key was used for six requests to each gateway.

| Gateway | Allowed | Denied | Behavior |
|---|---:|---:|---|
| `gateway-a` (`localhost:8080`) | 5 | 1 | Requests 1–5 returned 200; request 6 returned 429. |
| `gateway-b` (`localhost:8081`) | 5 | 1 | Requests 1–5 returned 200; request 6 returned 429. |
| Total | 10 | 2 | Each process enforced its own local bucket. |

The configured capacity is 5, but local fallback admitted 10 requests across two replicas. That is 5 extra requests, or 100% above the shared capacity, during this test. The result demonstrates the availability benefit and quota-drift cost of process-local fallback. It is not a worst-case bound: with more replicas, or higher request concurrency, aggregate over-admission can be larger.

The local responses marked the decision with `RateLimit-Degraded: local`, returned the local remaining count, and denied once each replica's bucket was empty.

## Fail-closed behavior

While Redis was stopped, `/v1/fail-closed-demo` returned HTTP 503 with `RateLimit-Degraded: fail_closed` and `rate_limiter_unavailable`. This confirms the request was rejected when the rule selected fail-closed.

## Redis recovery and shared quota

After Redis was restarted, `redis-cli ping` returned `PONG`. A fresh API key was sent first to gateway A and then gateway B. Both returned HTTP 200 without a degraded header; the remaining quota moved from 4 to 3. This confirms the gateways resumed using the shared Redis quota after recovery.

## Limits and next experiments

- The two-replica fallback count test and injected-latency load test use different identities and do not measure aggregate over-admission for one hot identity under concurrent load.
- The fail-open and fail-closed policies have not yet been compared under the same load profile for request outcomes and latency.
- Repeat outage tests at more gateway replica counts and compare all policies with the same request profile.
- Run the controlled stall experiment below and record recovery time and Prometheus counters.
- The in-flight cap has a unit test but has not yet been saturated in an end-to-end load experiment.

## Controlled Redis latency with Toxiproxy

Compose routes gateway Redis connections through Toxiproxy. The Toxiproxy admin API is bound to host loopback at `http://127.0.0.1:8474`; do not expose this unauthenticated control API to an untrusted network. The Redis proxy starts with no toxic and forwards to the Redis container.

Start or rebuild the stack and confirm the proxy is ready:

```bash
docker compose -f deploy/compose/docker-compose.yml up --build -d
curl -fsS http://localhost:8474/proxies
```

Add 25 ms response latency, above the gateway's 10 ms Redis timeout:

```bash
curl -fsS -X POST http://localhost:8474/proxies/redis/toxics \
  -H 'Content-Type: application/json' \
  -d '{"name":"redis_latency_down","type":"latency","stream":"downstream","toxicity":1.0,"attributes":{"latency":25,"jitter":0}}'
```

Send a few requests using one identity to each outage policy route. Check that requests return within the configured timeout budget plus gateway overhead, and inspect the degraded header and response code:

```bash
client_id="phase3-latency-$(date +%s)"
for i in $(seq 1 6); do
  curl -sS -i -H "X-API-Key: $client_id" http://localhost:8080/v1/local-fallback-demo
done
curl -sS -i -H "X-API-Key: $client_id" http://localhost:8080/v1/fail-closed-demo
```

Observed on gateway A with sequential requests:

| Request | Result | Total HTTP time |
|---:|---|---:|
| 1 | 200, local degraded, remaining 4 | 14.739 ms |
| 2 | 200, local degraded, remaining 3 | 12.669 ms |
| 3 | 200, local degraded, remaining 2 | 14.630 ms |
| 4 | 200, local degraded, remaining 1 | 13.357 ms |
| 5 | 200, local degraded, remaining 0 | 13.027 ms |
| 6 | 429, local degraded | 1.322 ms |

The first five measurements ranged from 12.669 to 14.739 ms with a configured 10 ms Redis timeout. The sixth returned faster after the breaker opened and the local fallback denied the exhausted bucket. These six sequential observations are not a latency benchmark and do not establish p95/p99. The fail-closed request on gateway B returned 503; the fail-open request on gateway A returned 200.

The gateway A metrics snapshot showed `rate_limiter_store_errors_total 6`, `rate_limiter_circuit_open_total 1`, and degraded decisions `fail_open/allow=1`, `local/allow=5`, `local/deny=1`. The fail-closed request was sent to gateway B, so its policy counter is on that replica's metrics endpoint and was not included in the gateway A snapshot.

## Concurrent load with 25 ms Redis response latency

With the same 25 ms downstream latency toxic active, Vegeta offered 100 requests/second for five seconds to each gateway separately. Each run used a different fresh API key and the local-fallback demo route.

| Gateway | Requests | HTTP 200 | HTTP 429 | 2xx ratio | Vegeta throughput | p50 | p95 | p99 | max |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| A | 500 | 14 | 486 | 2.80% | 2.81/s | 0.790 ms | 1.373 ms | 12.628 ms | 13.562 ms |
| B | 500 | 14 | 486 | 2.80% | 2.80/s | 0.775 ms | 1.362 ms | 12.368 ms | 12.822 ms |

No 5xx responses were reported; the only non-200 status was the expected 429. The 14 allowed requests per five-second run are close to the configured local bucket's initial capacity of five plus about ten refilled tokens over five seconds. These were separate identities and separate runs, so this table is not a shared-identity cross-replica comparison.

The aggregate latency percentiles are dominated by fast 429 responses (97.2% of the sample); they are not allowed-request latency percentiles. The roughly 12–13 ms p99/max captures the timeout/degraded transition under this test, while most denied requests completed much faster. This experiment validates behavior under an injected delay; it does not measure maximum gateway capacity or healthy Redis throughput.

## Same-profile comparison of all outage policies

Run `scripts/chaos/compare-failure-policies.sh` from WSL after the Compose stack is up and Vegeta is installed. It applies 25 ms downstream latency and sends each policy the same 100 requests/second for five seconds through gateway A. It restarts gateway A between policies to reset its in-memory circuit breaker and fallback bucket. The script removes the toxic and restarts the gateway on completion or interruption.

The script writes one raw Vegeta result and text report per policy plus a summary with gateway metrics under `docs/benchmarks/results/phase3-policy-comparison-<UTC run id>/`. It uses separate API keys for each policy. Compare HTTP status counts and latency distributions, and interpret them alongside the policy's intended behavior: fail-open should pass traffic, local should enforce only this replica's local bucket, and fail-closed should reject while Redis is unavailable. The 429/503 response rates are policy outcomes, not gateway capacity limits.

### Observed run: 2026-11-19

The saved run offered 100 requests/second for five seconds per policy through gateway A. It used a 25 ms downstream Redis latency toxic and a 10 ms Redis timeout. Gateway A was restarted between policies to reset its breaker and local state. Each policy had its own API key.

| Policy | Requests | HTTP 200 | HTTP 429 | HTTP 503 | 2xx ratio | 2xx throughput | p50 | p95 | p99 | max |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Fail-open | 500 | 500 | 0 | 0 | 100% | 100.17/s | 0.646 ms | 0.854 ms | 12.248 ms | 14.187 ms |
| Local fallback | 500 | 14 | 486 | 0 | 2.80% | 2.81/s | 0.666 ms | 0.996 ms | 12.176 ms | 13.269 ms |
| Fail-closed | 500 | 0 | 0 | 500 | 0% | 0/s | 0.816 ms | 1.510 ms | 12.495 ms | 13.631 ms |

All observed statuses match the configured policies: fail-open accepted every request during the Redis fault, local fallback admitted 14 requests and denied 486 at the configured local rate, and fail-closed returned its intentional 503 for every request. There were no unexpected status codes. The 2xx throughput is not a measure of gateway capacity; for local and fail-closed, most/all responses were intentional denials.

Each run reported 10 Redis store errors, 490 checks bypassing Redis while the circuit was open, and zero in-flight-cap rejections. The policy counters showed fail-open allow=500, local allow=14/deny=486, and fail-closed deny=500. This confirms the timeout and breaker redirected requests into each selected policy; the 256-check in-flight cap was not approached at this offered rate.

Latency percentiles cover all responses. For local fallback, 97.2% were 429, so its low p50/p95 describe fast denials, while p99 captures the timeout/probe transition. Do not interpret aggregate p99 as allowed-request latency. The run summary printed an empty Vegeta version value; capture the Vegeta build/module version on future runs to complete the environment record. Raw Vegeta files and per-policy reports are in `docs/benchmarks/results/phase3-policy-comparison-20261119T140113Z/`.

Inspect gateway metrics and the Grafana dashboard for Redis errors, degraded decisions, and open-circuit bypasses:

```bash
curl -sS http://localhost:8080/metrics | grep -E 'rate_limiter_(store_errors|circuit_open|redis_inflight_rejected|degraded_decisions)'
```

Remove the latency toxic and verify Redis responds:

```bash
curl -fsS -X DELETE http://localhost:8474/proxies/redis/toxics/redis_latency_down
docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli ping
```

After deleting the toxic, the proxy's `toxics` list was empty and `redis-cli ping` returned `PONG`. After a two-second wait, a fresh identity was sent to gateway A and then gateway B. Both returned 200 without a degraded header; remaining decreased from 9 to 8. This confirms Redis-backed quota recovery and cross-replica sharing after the latency injection.

A Redis command can execute even if its response arrives after the client timeout; use a fresh identity when measuring post-latency quota behavior.

## Stalled Redis responses with Toxiproxy

The Toxiproxy `timeout` toxic with `timeout: 0` drops data without closing the connection until the toxic is removed. This creates a stalled response path while Redis may still process the command. See [Toxiproxy's timeout toxic documentation](https://github.com/Shopify/toxiproxy#timeout).

Add a downstream stall:

```bash
curl -fsS -X POST http://localhost:8474/proxies/redis/toxics \
  -H 'Content-Type: application/json' \
  -d '{"name":"redis_stall_down","type":"timeout","stream":"downstream","toxicity":1.0,"attributes":{"timeout":0}}'
```

Measure requests against the local policy and both other policies. Use new identities so the earlier latency run does not affect results:

```bash
stall_id="phase3-stall-$(date +%s)"
for i in $(seq 1 6); do
  curl -sS -D /tmp/rl-headers -o /tmp/rl-response \
    -w "local request $i: HTTP %{http_code}, total %{time_total}s\n" \
    -H "X-API-Key: $stall_id" http://localhost:8080/v1/local-fallback-demo
  grep -iE '^(HTTP/|ratelimit-degraded:|ratelimit-remaining:|retry-after:)' /tmp/rl-headers
done
curl -sS -i -H "X-API-Key: ${stall_id}-closed" http://localhost:8081/v1/fail-closed-demo
curl -sS -i -H "X-API-Key: ${stall_id}-open" http://localhost:8080/v1/resource
```

Then remove the toxic immediately and verify recovery:

```bash
curl -fsS -X DELETE http://localhost:8474/proxies/redis/toxics/redis_stall_down
curl -fsS http://localhost:8474/proxies
docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli ping
```

Observed stall results on gateway A: the first five local fallback checks returned 200 in 12.751, 13.084, 14.769, 13.814, and 13.307 ms; the sixth returned 429 in 1.450 ms. The fail-closed route on gateway B returned 503, and the fail-open route on gateway A returned 200. After removing the toxic, the proxy reported an empty toxic list and Redis returned `PONG`.

This is a sequential functional result, not a percentile benchmark. After the toxic was removed and the breaker cooldown elapsed, a fresh key returned 200 through gateway A with remaining 9 and through gateway B with remaining 8, without a degraded header. This confirms the shared Redis quota recovered after the stall. Because a stalled response may follow an executed Redis command, use a fresh identity for recovery checks.

## Sustained stalled-response soak

The gateway now exposes Go runtime and process collectors, including `go_goroutines`, `go_memstats_heap_alloc_bytes`, and `process_resident_memory_bytes`. Run `scripts/chaos/redis-stall-soak.sh` from WSL after rebuilding the gateways and starting Compose. By default, it sends 100 requests/second for 60 seconds through the local-fallback route while Toxiproxy drops Redis responses indefinitely. Override with `RATE` or `DURATION` only when recording those values in the result.

The script samples goroutine count, allocated heap, and resident memory every two seconds, captures the Vegeta report and resilience counters, removes the toxic, waits for the breaker cooldown, and sends fresh-identity recovery requests through both replicas. It writes the samples, raw Vegeta results, and recovery output under `docs/benchmarks/results/phase3-stall-soak-<UTC run id>/`.

Evaluate resource samples as a time series: temporary heap and resident-memory changes are normal, but goroutines should return near baseline and memory should settle rather than grow continuously throughout the stall. This soak has not been run yet. The 100 requests/second default is intended for resource-growth observation; it is not high enough to saturate the 256-check Redis in-flight cap, which remains covered only by a unit test.
