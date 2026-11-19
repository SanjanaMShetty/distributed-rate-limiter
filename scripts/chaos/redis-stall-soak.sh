#!/usr/bin/env bash
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."

compose=(docker compose -f deploy/compose/docker-compose.yml)
toxiproxy_api="http://localhost:8474"
rate="${RATE:-100/s}"
duration="${DURATION:-60s}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)"
results_dir="docs/benchmarks/results/phase3-stall-soak-${run_id}"
mkdir -p "$results_dir"

if ! command -v vegeta >/dev/null 2>&1; then
	echo "vegeta is required; install it before running this experiment" >&2
	exit 1
fi
curl -fsS "$toxiproxy_api/proxies" >/dev/null
curl -fsS http://localhost:8080/healthz >/dev/null

remove_toxic() {
	curl -fsS -X DELETE "$toxiproxy_api/proxies/redis/toxics/redis_stall_down" >/dev/null 2>&1 || true
}

cleanup() { remove_toxic; }
trap cleanup EXIT

sample_runtime() {
	metrics="$(curl -fsS --max-time 2 http://localhost:8080/metrics)"
	goroutines="$(awk '$1 == "go_goroutines" { print $2; exit }' <<< "$metrics")"
	heap="$(awk '$1 == "go_memstats_heap_alloc_bytes" { print $2; exit }' <<< "$metrics")"
	rss="$(awk '$1 == "process_resident_memory_bytes" { print $2; exit }' <<< "$metrics")"
	if [[ -z "$goroutines" || -z "$heap" || -z "$rss" ]]; then
		echo "Go runtime or process metrics are missing; rebuild the gateways with the runtime collectors enabled" >&2
		return 1
	fi
	printf '%s,%s,%s,%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$goroutines" "$heap" "$rss" >> "$results_dir/runtime-samples.csv"
}

remove_toxic
{
	echo "# Redis stalled-response soak"
	echo
	echo "- Run ID (UTC): ${run_id}"
	echo "- Offered load: ${rate} for ${duration}"
	echo "- Redis timeout: 10 ms"
	echo "- Toxiproxy: downstream timeout toxic, timeout=0 (responses dropped until toxic removal)"
	echo "- Route: /v1/local-fallback-demo"
	echo "- Host kernel: $(uname -srmo)"
	echo "- Logical CPUs: $(nproc)"
	echo "- Go: $(go version 2>/dev/null || echo unavailable)"
	echo "- Vegeta build: $(go version -m "$(command -v vegeta)" 2>/dev/null | sed -n '1,5p' | tr '\n' '; ')"
	echo
} > "$results_dir/summary.md"
printf 'timestamp_utc,go_goroutines,heap_alloc_bytes,resident_memory_bytes\n' > "$results_dir/runtime-samples.csv"

sample_runtime
curl -fsS -X POST "$toxiproxy_api/proxies/redis/toxics" \
	-H 'Content-Type: application/json' \
	-d '{"name":"redis_stall_down","type":"timeout","stream":"downstream","toxicity":1.0,"attributes":{"timeout":0}}' >/dev/null

client_id="phase3-stall-soak-${run_id}"
printf 'GET http://localhost:8080/v1/local-fallback-demo\n' |
	vegeta attack -rate="$rate" -duration="$duration" -workers=64 -max-workers=256 \
		-header="X-API-Key: ${client_id}" -output="$results_dir/stall-soak.bin" &
attack_pid=$!

while kill -0 "$attack_pid" 2>/dev/null; do
	sample_runtime
	sleep 2
done
wait "$attack_pid"
sample_runtime

vegeta report "$results_dir/stall-soak.bin" > "$results_dir/vegeta-report.txt"
{
	echo "## Vegeta report"
	echo
	cat "$results_dir/vegeta-report.txt"
	echo
	echo "## Gateway A resilience counters during stall"
	echo
	curl -fsS http://localhost:8080/metrics |
		grep -E 'rate_limiter_(store_errors_total|circuit_open_total|redis_inflight_rejected_total|degraded_decisions_total)' || true
} >> "$results_dir/summary.md"

remove_toxic
curl -fsS http://localhost:8474/proxies > "$results_dir/toxiproxy-after.json"
sleep 2
recovery_id="phase3-stall-recovery-${run_id}"
{
	echo "## Recovery requests"
	echo
	for port in 8080 8081; do
		echo "### Gateway port ${port}"
		curl -sS -i -H "X-API-Key: ${recovery_id}" "http://localhost:${port}/v1/resource"
		echo
	done
	"${compose[@]}" exec -T redis redis-cli ping
	echo
} > "$results_dir/recovery.txt"

sample_runtime
trap - EXIT
echo "Stall soak artifacts saved in ${results_dir}"
echo "Latency toxic removed; inspect recovery.txt and runtime-samples.csv."
