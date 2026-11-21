# Phase 4: performance and observability

Phase 4 produces repeatable measurements for the embedded Go engine with healthy Redis. Run the benchmark from WSL against the Compose gateways; Toxiproxy is in the normal Redis path, so it adds a local proxy hop even when no toxic is active. These results describe this local topology and are not production capacity claims.

## Start and check the stack

```bash
docker compose -f deploy/compose/docker-compose.yml up --build -d
docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli ping
curl -fsS http://localhost:8474/proxies
curl -fsS http://localhost:9090/-/ready
```

The Redis response should be `PONG`, Prometheus should be ready, and the Redis proxy's `toxics` array should be empty. If a chaos test left a toxic behind, remove it before benchmarking. The two benchmark routes are intentionally high-capacity load-test rules and are not examples of recommended customer quotas.

## Workload profiles

The script creates a unique run directory under `docs/benchmarks/results/` with the raw Vegeta file, text report, metadata, gateway metric snapshots, and before/after Redis stats, memory, and CPU snapshots.

```bash
bash scripts/bench/run-profile.sh baseline
bash scripts/bench/run-profile.sh allow-hot
bash scripts/bench/run-profile.sh allow-many
bash scripts/bench/run-profile.sh mixed
bash scripts/bench/run-profile.sh deny-heavy
bash scripts/bench/run-profile.sh algo-token
bash scripts/bench/run-profile.sh algo-fixed
bash scripts/bench/run-profile.sh algo-sliding
```

Defaults are 100 requests/second for 30 seconds, sent to gateway A. Set `GATEWAY=http://localhost:8081` to target gateway B, or set `RATE` and `DURATION` to change the load. For example:

```bash
GATEWAY=http://localhost:8080 RATE=500/s DURATION=60s bash scripts/bench/run-profile.sh allow-hot
```

Profiles:

| Profile | Route and identity pattern | What it stresses |
|---|---|---|
| `baseline` | Isolated local HTTP responder, same success path and API-key header | Gateway/HTTP response cost without the limiter or Redis; listens only on host loopback port 8082 |
| `allow-hot` | High-capacity token bucket, one API key | One hot Redis key while nearly all calls are allowed |
| `allow-many` | High-capacity token bucket, target file with up to 10,000 API keys | Redis operations and key churn spread across identities; the number actually used is bounded by requests sent |
| `mixed` | Capacity 100, refill 50/s, one API key | Allow and deny behavior near 100 requests/s after the initial burst |
| `deny-heavy` | Standard 10-capacity, 2/s rule, pre-exhausted key | Fast rejection path after quota exhaustion |
| `algo-token` | High-capacity token bucket | Algorithm comparison under the same allow-heavy offered load |
| `algo-fixed` | High-capacity fixed window | Algorithm comparison under the same allow-heavy offered load |
| `algo-sliding` | High-capacity sliding-window counter | Algorithm comparison under the same allow-heavy offered load |

Use the same host, image versions, Redis state, load rate, duration, and gateway target for comparisons. First run each profile once to warm containers, then collect at least three recorded runs per rate. A useful initial rate sweep is 100, 500, and 1,000 requests/second; stop increasing the offered load if the generator cannot sustain it or the gateway begins returning unexpected errors. Compare the median and spread across runs, not only the best result.

For a paired added-latency comparison, run baseline and `allow-hot` at the same rate and duration, alternating the order for repeated pairs:

```bash
docker compose -f deploy/compose/docker-compose.yml up --build -d
RATE=100/s DURATION=30s bash scripts/bench/run-profile.sh baseline
RATE=100/s DURATION=30s bash scripts/bench/run-profile.sh allow-hot
```

The baseline uses a separate minimal Go HTTP process, not an unprotected route in the gateway. Compare p50/p95/p99 from the paired runs; call the difference an observed local overhead estimate, since the baseline still cannot perfectly model every gateway middleware and container scheduling cost.

For a Redis algorithm comparison, the three `algo-*` profiles use the same gateway, high quota, identity pattern, offered load, and duration. Run them at the same rate and alternate order over at least three rounds. These profiles measure the allow path; they do not compare the algorithms' policy behavior or their denied-request costs. Their high configured capacities are benchmark settings only.

```bash
for round in 1 2 3; do
  for profile in algo-token algo-fixed algo-sliding; do
    RATE=500/s DURATION=30s bash scripts/bench/run-profile.sh "$profile"
  done
done
```

## Read the results correctly

- Vegeta `rate` is offered load; `throughput` is completed responses per second. `Success` counts successful 2xx responses, so a deny-heavy profile is expected to have low success and many 429s.
- Report p50/p95/p99 alongside status counts. In mixed and deny-heavy profiles, aggregate latency includes 429 responses. It does not represent allowed-request latency alone.
- `allow-hot` is deliberately one key and can become Redis's hot-key ceiling. `allow-many` spreads requests across keys and uses a generated target file; it also creates Redis key state until TTL expiry.
- Gateway metric snapshots are cumulative process counters. For per-run counts use the raw Vegeta report, or compare before/after metric snapshots. Prometheus/Grafana rate panels are useful for time-series shape but scrape resolution and counter history affect short runs.
- Capture the result directory name and exact command with any number quoted in the README. Do not claim million requests/second from an offered rate or from a local Docker benchmark.

## Grafana and Prometheus alerts

Open Grafana at `http://localhost:3000`, dashboard **Distributed Rate Limiter**. It shows engine decisions, engine decision latency, HTTP status rate, end-to-end p95/p99, Redis errors/degraded decisions, Redis pool connections/waiters/timeouts, CPU, memory, goroutines, and target health. Pool gauges are sampled from go-redis `PoolStats`; its hit, miss, timeout, and wait-duration values are cumulative since process start. Prometheus evaluates the rules in `deploy/compose/alert_rules.yml`; inspect `http://localhost:9090/alerts` to see active or pending alerts. This local setup does not configure Alertmanager delivery, so rules are visible in Prometheus but do not send notifications.

To verify alert firing without disrupting both replicas, stop only gateway B and allow the rule's 30-second pending period plus a Prometheus evaluation interval to pass. Confirm `RateLimiterGatewayUnavailable` is firing at `/api/v1/alerts`, then restart gateway B and wait for the alert to clear:

```bash
docker compose -f deploy/compose/docker-compose.yml stop gateway-b
sleep 75
curl -fsS http://localhost:9090/api/v1/alerts
docker compose -f deploy/compose/docker-compose.yml start gateway-b
```

Run the check only when the local Compose stack is idle; it intentionally removes one gateway target temporarily.

## Phase 4 completion evidence

- At least three raw runs per workload and rate, with metadata and median/spread summarized in a results table.
- Healthy Redis and both gateways scrape successfully during the run; no unexpected 5xx responses.
- Dashboard panels show throughput, status mix, p95/p99 latency, CPU, memory, and target health.
- Alert rules load and can be observed as pending/firing during controlled conditions.
- Same-load benchmark results exist for the token bucket, fixed window, and sliding-window counter, with at least three runs each.
- Results explicitly describe the local Compose setup and avoid generalizing local numbers into cloud production capacity.

The initial 100/s profile results, exploratory `allow-hot` rate sweep, and first no-op baseline pair are recorded in [`phase4-results.md`](phase4-results.md). The dashboard and Prometheus rule loading have been verified; repeated baseline pairs, alert firing behavior, and identical-load algorithm comparisons remain outstanding.
