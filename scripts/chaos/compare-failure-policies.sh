#!/usr/bin/env bash
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."

compose=(docker compose -f deploy/compose/docker-compose.yml)
toxiproxy_api="http://localhost:8474"
run_id="$(date -u +%Y%m%dT%H%M%SZ)"
results_dir="docs/benchmarks/results/phase3-policy-comparison-${run_id}"
mkdir -p "$results_dir"

if ! command -v vegeta >/dev/null 2>&1; then
	echo "vegeta is required; install it before running this experiment" >&2
	exit 1
fi
curl -fsS "$toxiproxy_api/proxies" >/dev/null
curl -fsS http://localhost:8080/healthz >/dev/null

remove_toxic() {
	curl -fsS -X DELETE "$toxiproxy_api/proxies/redis/toxics/redis_latency_down" >/dev/null 2>&1 || true
}

cleanup() {
	remove_toxic
	"${compose[@]}" restart gateway-a >/dev/null 2>&1 || true
}
trap cleanup EXIT

restart_gateway_a() {
	"${compose[@]}" restart gateway-a >/dev/null
	for _ in $(seq 1 30); do
		if curl -fsS --max-time 1 http://localhost:8080/healthz >/dev/null; then
			return 0
		fi
		sleep 0.2
	done
	echo "gateway-a did not become healthy after restart" >&2
	return 1
}

remove_toxic
curl -fsS -X POST "$toxiproxy_api/proxies/redis/toxics" \
	-H 'Content-Type: application/json' \
	-d '{"name":"redis_latency_down","type":"latency","stream":"downstream","toxicity":1.0,"attributes":{"latency":25,"jitter":0}}' >/dev/null

{
	echo "# Phase 3 outage policy comparison"
	echo
	echo "- Run ID (UTC): ${run_id}"
	echo "- Offered load: 100 requests/second for 5 seconds per policy"
	echo "- Redis timeout: 10 ms"
	echo "- Toxiproxy: 25 ms downstream latency"
	echo "- Target: gateway A on localhost:8080"
	echo "- Host kernel: $(uname -srmo)"
	echo "- Logical CPUs: $(nproc)"
	echo "- Go: $(go version 2>/dev/null || echo unavailable)"
	echo "- Vegeta: $(vegeta -version 2>&1 | head -n 1)"
	echo
} > "$results_dir/summary.md"

run_case() {
	policy="$1"
	path="$2"
	client_id="phase3-${policy}-${run_id}"
	result_file="$results_dir/${policy}.bin"

	restart_gateway_a
	{
		echo "## ${policy}"
		echo
		printf '%s\n' "Route: ${path}; API key: ${client_id}"
		echo
	} >> "$results_dir/summary.md"

	printf 'GET http://localhost:8080%s\n' "$path" |
		vegeta attack -rate=100/s -duration=5s -workers=20 -max-workers=20 \
			-header="X-API-Key: ${client_id}" -output="$result_file"

	vegeta report "$result_file" | tee "$results_dir/${policy}.report.txt" >> "$results_dir/summary.md"
	{
		echo
		echo "Gateway A counters after ${policy}:"
		curl -fsS http://localhost:8080/metrics |
			grep -E 'rate_limiter_(store_errors_total|fail_open_total|circuit_open_total|redis_inflight_rejected_total|degraded_decisions_total)' || true
		echo
	} >> "$results_dir/summary.md"
}

run_case fail_open /v1/resource
run_case local /v1/local-fallback-demo
run_case fail_closed /v1/fail-closed-demo

remove_toxic
restart_gateway_a
trap - EXIT

echo "Policy comparison saved in ${results_dir}"
echo "Toxiproxy latency toxic removed and gateway-a restarted."
