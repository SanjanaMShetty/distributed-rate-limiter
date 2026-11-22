#!/usr/bin/env bash
set -Eeuo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/../.."

rate="${1:-100/s}"
duration="${2:-30s}"
if [[ ! "$rate" =~ ^[0-9]+(/s|/m|/h)?$ ]]; then
	echo "rate must look like 100/s" >&2
	exit 2
fi
if [[ ! "$duration" =~ ^[0-9]+(ms|s|m|h)$ ]]; then
	echo "duration must look like 30s or 2m" >&2
	exit 2
fi

namespace="${NAMESPACE:-rate-limiter}"
image="distributed-rate-limiter-loadgen:phase5"
cluster="${KIND_CLUSTER:-rate-limiter}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
client_id="k8s-load-${run_id}"
job="rate-limiter-load-${run_id,,}"
results_dir="docs/benchmarks/results/k8s-phase5-${run_id}"
mkdir -p "$results_dir"

current_context="$(kubectl config current-context)"
expected_context="kind-${cluster}"
if [[ "$current_context" != "$expected_context" ]]; then
	echo "refusing clear-text load test: expected Kubernetes context $expected_context, got $current_context" >&2
	exit 2
fi
service_type="$(kubectl -n "$namespace" get service rate-limiter-distributed-rate-limiter -o jsonpath='{.spec.type}')"
if [[ "$service_type" != "ClusterIP" ]]; then
	echo "refusing clear-text load test: expected an internal ClusterIP Service, got $service_type" >&2
	exit 2
fi
docker build -f Dockerfile.loadgen -t "$image" .
kind load docker-image "$image" --name "$cluster"

# SECURITY REVIEW: This load generator targets only the fixed ClusterIP service DNS name
# inside the Kind cluster. The benchmark route uses plain HTTP and a generated fake API key;
# no real credentials or user traffic are sent. Do not expose this Service or run this job
# against an untrusted network. TLS belongs at the production ingress boundary, outside this
# private in-cluster synthetic benchmark.
command="printf 'GET http://rate-limiter-distributed-rate-limiter/v1/benchmark-allow\\nX-API-Key: ${client_id}\\n\\n' | vegeta attack -rate=${rate} -duration=${duration} -workers=64 -max-workers=256 | vegeta report"
kubectl -n "$namespace" create job "$job" --image="$image" -- /bin/sh -c "$command"

set +e
kubectl -n "$namespace" wait --for=condition=complete --timeout=10m "job/$job"
wait_status=$?
set -e

{
	echo "# Phase 5 in-cluster load run"
	echo
	echo "- Run ID: ${run_id}"
	echo "- Namespace: ${namespace}"
	echo "- Offered load: ${rate} for ${duration}"
	echo "- Target: rate-limiter-distributed-rate-limiter/v1/benchmark-allow"
	echo "- Gateway replicas: $(kubectl -n "$namespace" get deployment rate-limiter-distributed-rate-limiter -o jsonpath='{.status.readyReplicas}')"
	echo
	kubectl -n "$namespace" logs "job/$job"
} | tee "$results_dir/summary.md"

kubectl -n "$namespace" delete job "$job" --ignore-not-found
exit "$wait_status"
