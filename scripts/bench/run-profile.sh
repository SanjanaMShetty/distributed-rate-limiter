#!/usr/bin/env bash
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."

profile="${1:-}"
if [[ -z "$profile" ]]; then
	echo "usage: $0 {baseline|allow-hot|allow-many|mixed|deny-heavy|algo-token|algo-fixed|algo-sliding}" >&2
	exit 2
fi

if [[ "$profile" == "baseline" ]]; then
	gateway="${GATEWAY:-http://localhost:8082}"
else
	gateway="${GATEWAY:-http://localhost:8080}"
fi
rate="${RATE:-100/s}"
duration="${DURATION:-30s}"
keys="${KEYS:-10000}"
identity_count=1
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
results_dir="docs/benchmarks/results/phase4-${profile}-${run_id}"
mkdir -p "$results_dir"

if ! command -v vegeta >/dev/null 2>&1; then
	echo "vegeta is required; install it before running this benchmark" >&2
	exit 1
fi
if ! curl -fsS "$gateway/healthz" >/dev/null; then
	echo "gateway is not healthy at $gateway" >&2
	exit 1
fi
if [[ "$profile" != "baseline" ]]; then
	if ! docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli ping | grep -qx PONG; then
		echo "Redis is not responding; start the Compose stack first" >&2
		exit 1
	fi
	proxy_state="$(curl -fsS http://localhost:8474/proxies)"
	if [[ "$proxy_state" != *'"toxics":[]'* ]]; then
		echo "Toxiproxy is unavailable or has active toxics; restore a clean proxy before benchmarking" >&2
		echo "$proxy_state" >&2
		exit 1
	fi
fi

client_id="phase4-${profile}-${run_id}"
target_file="$results_dir/targets.txt"
case "$profile" in
	baseline)
		path="/v1/benchmark-allow"
		printf 'GET %s%s\n' "$gateway" "$path" > "$target_file"
		printf 'X-API-Key: %s\n\n' "$client_id" >> "$target_file"
		;;
	allow-hot)
		path="/v1/benchmark-allow"
		printf 'GET %s%s\n' "$gateway" "$path" > "$target_file"
		printf 'X-API-Key: %s\n\n' "$client_id" >> "$target_file"
		;;
	algo-token)
		path="/v1/benchmark-token-bucket"
		printf 'GET %s%s\n' "$gateway" "$path" > "$target_file"
		printf 'X-API-Key: %s\n\n' "$client_id" >> "$target_file"
		;;
	algo-fixed)
		path="/v1/benchmark-fixed-window"
		printf 'GET %s%s\n' "$gateway" "$path" > "$target_file"
		printf 'X-API-Key: %s\n\n' "$client_id" >> "$target_file"
		;;
	algo-sliding)
		path="/v1/benchmark-sliding-window"
		printf 'GET %s%s\n' "$gateway" "$path" > "$target_file"
		printf 'X-API-Key: %s\n\n' "$client_id" >> "$target_file"
		;;
	allow-many)
		path="/v1/benchmark-allow"
		identity_count="$keys"
		: > "$target_file"
		for i in $(seq 1 "$keys"); do
			printf 'GET %s%s\nX-API-Key: %s-%s\n\n' "$gateway" "$path" "$client_id" "$i" >> "$target_file"
		done
		;;
	mixed)
		path="/v1/benchmark-mixed"
		printf 'GET %s%s\n' "$gateway" "$path" > "$target_file"
		printf 'X-API-Key: %s\n\n' "$client_id" >> "$target_file"
		;;
	deny-heavy)
		path="/v1/resource"
		for _ in $(seq 1 30); do
			curl -sS -o /dev/null -H "X-API-Key: $client_id" "$gateway$path" || true
		done
		printf 'GET %s%s\n' "$gateway" "$path" > "$target_file"
		printf 'X-API-Key: %s\n\n' "$client_id" >> "$target_file"
		;;
	*)
		echo "unknown profile: $profile" >&2
		echo "usage: $0 {baseline|allow-hot|allow-many|mixed|deny-heavy|algo-token|algo-fixed|algo-sliding}" >&2
		exit 2
		;;
esac

{
	echo "# Phase 4 benchmark: ${profile}"
	echo
	echo "- Run ID (UTC): ${run_id}"
	echo "- Gateway URL: ${gateway}"
	echo "- Profile: ${profile}"
	echo "- Offered load: ${rate} for ${duration}"
	echo "- Unique identities in target file: ${identity_count}"
	echo "- Host kernel: $(uname -srmo)"
	echo "- Logical CPUs: $(nproc)"
	echo "- Go: $(go version 2>/dev/null || echo unavailable)"
	echo "- Vegeta build: $(go version -m "$(command -v vegeta)" 2>/dev/null | sed -n '1,5p' | tr '\n' '; ')"
	if [[ "$profile" == "baseline" ]]; then
		echo "- Redis: not used by the no-op HTTP baseline"
	else
		echo "- Redis: $(docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-server --version 2>/dev/null | tr -d '\r')"
	fi
	echo "- Git revision: $(git rev-parse HEAD 2>/dev/null || echo unavailable)"
	echo
} > "$results_dir/metadata.md"

if [[ "$profile" == "baseline" ]]; then
	echo "Metrics endpoint not implemented for the isolated baseline server." > "$results_dir/metrics-before.txt"
	started_epoch="$(date +%s)"
	vegeta attack -targets="$target_file" -rate="$rate" -duration="$duration" \
		-workers=64 -max-workers=256 -output="$results_dir/results.bin"
	ended_epoch="$(date +%s)"
	echo "Redis is not used by the baseline server." > "$results_dir/redis-stats-before.txt"
	echo "Redis is not used by the baseline server." > "$results_dir/redis-memory-before.txt"
	echo "Redis is not used by the baseline server." > "$results_dir/redis-cpu-before.txt"
	vegeta report "$results_dir/results.bin" > "$results_dir/report.txt"
	echo "Metrics endpoint not implemented for the isolated baseline server." > "$results_dir/metrics-after.txt"
	echo "Redis is not used by the baseline server." > "$results_dir/redis-stats-after.txt"
	echo "Redis is not used by the baseline server." > "$results_dir/redis-memory-after.txt"
	echo "Redis is not used by the baseline server." > "$results_dir/redis-cpu-after.txt"
else
	curl -fsS "$gateway/metrics" > "$results_dir/metrics-before.txt"
	docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli INFO stats > "$results_dir/redis-stats-before.txt"
	docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli INFO memory > "$results_dir/redis-memory-before.txt"
	docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli INFO CPU > "$results_dir/redis-cpu-before.txt"
	started_epoch="$(date +%s)"
	vegeta attack -targets="$target_file" -rate="$rate" -duration="$duration" \
		-workers=64 -max-workers=256 -output="$results_dir/results.bin"
	ended_epoch="$(date +%s)"
	vegeta report "$results_dir/results.bin" > "$results_dir/report.txt"
	curl -fsS "$gateway/metrics" > "$results_dir/metrics-after.txt"
	docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli INFO stats > "$results_dir/redis-stats-after.txt"
	docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli INFO memory > "$results_dir/redis-memory-after.txt"
	docker compose -f deploy/compose/docker-compose.yml exec -T redis redis-cli INFO CPU > "$results_dir/redis-cpu-after.txt"
fi
{
	cat "$results_dir/metadata.md"
	echo "- Wall-clock benchmark duration (seconds): $((ended_epoch - started_epoch))"
	if [[ "$profile" == "baseline" ]]; then
		echo "- Redis snapshots: not applicable to baseline"
	else
		echo "- Redis INFO stats, memory, and CPU snapshots: redis-*-before.txt / redis-*-after.txt"
	fi
	echo
	cat "$results_dir/report.txt"
} > "$results_dir/summary.md"

echo "Benchmark artifacts saved in $results_dir"
if [[ "$profile" == "baseline" ]]; then
	echo "This isolated HTTP baseline does not use Redis or Toxiproxy."
else
	echo "Metrics snapshots and raw Vegeta results are included; Toxiproxy was checked for active toxics."
fi
