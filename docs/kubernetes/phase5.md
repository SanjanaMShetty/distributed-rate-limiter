# Phase 5: Kubernetes, GitOps, and operations

Phase 5 moves the gateway and Redis-backed shared quota into Kubernetes, then validates service behavior during load, pod disruptions, and CPU-based horizontal scaling. It also adds CI image scanning and GitOps delivery with Argo CD. The local kind cluster is for repeatable development evidence, not production capacity claims.

## First milestone: deploy locally with Helm

The chart is in `deploy/k8s/helm/distributed-rate-limiter`. It creates two gateway pods behind a ClusterIP Service, a single in-cluster Redis pod, rules from Helm values, readiness/liveness/startup probes, resource requests and limits, non-root gateway security settings, a PodDisruptionBudget, and a NetworkPolicy restricting Redis ingress to the gateway pods. A checksum annotation on the gateway pod template forces a rollout when Helm-managed rules change because the process reads rules at startup. Gateway count defaults to two fixed replicas; the optional CPU HPA is enabled for the GitOps deployment. The optional ServiceMonitor requires the Prometheus Operator API.

The chart's Redis pod is a single ephemeral instance with persistence disabled. It is for local scaling experiments; it is not a highly available production Redis deployment. All gateway replicas share that Redis service, so their quota state is shared.

### Prerequisites

- Docker Desktop running with its WSL integration enabled (kind uses Docker as its node runtime).
- `kubectl` configured for that cluster.
- Helm 3 installed.
- Go installed (for installing kind).

### Build and install

Install the pinned kind release, create a three-node local cluster (one control plane and two workers), and verify its context:

```bash
go install sigs.k8s.io/kind@v0.33.0
export PATH="$(go env GOPATH)/bin:$PATH"
kind create cluster --name rate-limiter --config deploy/k8s/kind-config.yaml --wait 2m
kubectl config use-context kind-rate-limiter
kubectl get nodes
```

From the repository root, build the gateway image and load it into kind's container nodes before installing the chart:

```bash
docker build -t distributed-rate-limiter:phase5 -f Dockerfile .
kind load docker-image distributed-rate-limiter:phase5 --name rate-limiter
helm lint deploy/k8s/helm/distributed-rate-limiter
helm template rate-limiter deploy/k8s/helm/distributed-rate-limiter --namespace rate-limiter
helm upgrade --install rate-limiter deploy/k8s/helm/distributed-rate-limiter \
  --namespace rate-limiter --create-namespace
kubectl -n rate-limiter get pods,svc,pdb,networkpolicy
kubectl -n rate-limiter rollout status deployment/rate-limiter-distributed-rate-limiter
```

Forward the gateway service in a second terminal and exercise its shared quota:

```bash
kubectl -n rate-limiter port-forward svc/rate-limiter-distributed-rate-limiter 8080:80
```

```bash
client_id="k8s-demo-$(date +%s)"
for i in $(seq 1 7); do
  curl -sS -o /tmp/rl-response -w "request $i: HTTP %{http_code}\n" \
    -H "X-API-Key: $client_id" http://localhost:8080/v1/phase1-demo
  cat /tmp/rl-response
done
```

The first five requests should be allowed and the following requests denied, subject to the two-token-per-second refill. To prove requests are reaching different pods, inspect `kubectl -n rate-limiter get pods -o wide` and gateway request metrics while sending a longer run. A Kubernetes Service load-balances connections; one persistent curl/HTTP connection may repeatedly reach the same pod.

## Scaling and disruption experiments

Build the in-cluster Vegeta image and run a load job against the Kubernetes Service:

```bash
bash scripts/k8s/run-loadgen.sh 500/s 60s
```

The script builds a pinned Vegeta image, loads it into kind, creates a one-shot Job, prints its report, and saves a summary under `docs/benchmarks/results/`. Use another terminal while it runs to delete one gateway pod and check that load continues through the other ready pod. Record curated results in [`phase5-results.md`](phase5-results.md).

After the first deployment is healthy, compare 1, 2, and 4 gateway replicas at identical offered loads. Keep Redis constant and record throughput, p50/p95/p99, 5xx rate, CPU/memory, Redis operations, and gateway pod count. Delete one gateway pod during load and verify the service continues through the other ready pod. Then test a rolling image update and confirm the PodDisruptionBudget keeps one gateway available.

## CPU-based horizontal scaling

The chart defines an optional CPU HPA (`autoscaling/v2`) with a 60% average CPU target, minimum 2 and maximum 8 gateway pods. CPU utilization is measured as a percentage of the pod CPU request; the gateway request is currently `100m`, so the target corresponds to roughly `60m` average CPU per pod. The HPA scales gateway pods only. Redis remains one shared pod and may become the limiting component. The HPA allows immediate scale-up decisions and stabilizes scale-down for three minutes to avoid rapid replica churn.

Metrics Server must be available: `kubectl top pods -n rate-limiter` should return CPU and memory values. For a direct Helm deployment, enable the HPA with:

```bash
helm upgrade --install rate-limiter deploy/k8s/helm/distributed-rate-limiter \
  --namespace rate-limiter --create-namespace \
  --set autoscaling.enabled=true \
  --set autoscaling.minReplicas=2 \
  --set autoscaling.maxReplicas=8 \
  --set autoscaling.targetCPUUtilizationPercentage=60
kubectl -n rate-limiter get hpa -w
```

Run the load generator in a second terminal. In the first terminal, watch the HPA and replica count:

```bash
watch -n 5 'kubectl -n rate-limiter get hpa; kubectl -n rate-limiter get deployment rate-limiter-distributed-rate-limiter; kubectl top pods -n rate-limiter'
```

Start at 1,000 requests/second for 2 minutes; if CPU stays below target, increase the offered rate in measured steps and stop if Redis or host resources become saturated:

```bash
bash scripts/k8s/run-loadgen.sh 1000/s 120s
```

Afterward, record peak replicas and wait for the HPA to scale down before recording the final replica count.

An idle reading such as `1%/60%` with two replicas only proves the HPA can read metrics; it does not prove that it scales. Record the offered rate, CPU target/current value, desired and ready replica counts over time, p95/p99 latency, HTTP errors, and Redis resource use. If the HPA does not scale before Redis saturates, report that result: it shows CPU is not a sufficient demand signal for this workload. Request-rate scaling would require a custom or external metrics API adapter and is a separate extension, not a drop-in setting.

## Monitoring

The chart can emit a Prometheus Operator `ServiceMonitor` when `serviceMonitor.enabled=true`. Install `kube-prometheus-stack`, set the ServiceMonitor labels to match that Prometheus instance's selector, and confirm both gateway pods appear as scrape targets. The checked-in dashboard queries select either the Compose gateway job or the Kubernetes gateway job. Update the dashboard ConfigMap in the monitoring namespace after changing the JSON:

```bash
kubectl create configmap rate-limiter-dashboard \
  --from-file=rate-limiter.json=deploy/compose/grafana/dashboards/rate-limiter.json \
  -n monitoring --dry-run=client -o yaml |
  kubectl label --local -f - grafana_dashboard=1 -o yaml |
  kubectl apply -f -
```

Refresh the Grafana dashboard and verify the scrape-health panel shows both gateway instances.

## CI, Trivy, and Argo CD

The GitHub Actions workflow at `.github/workflows/ci.yml` runs Go tests with the race detector, `go vet`, a build, Helm lint, checks that the HPA renders when enabled, and Trivy scans for the source tree and built container. High and critical findings with available fixes fail the workflow. On pushes to `main`, the exact scanned image artifact is published to GHCR with an immutable `sha-<full-commit>` tag. Review scan findings in the Actions run; do not claim a clean scan unless the run confirms it.

Argo CD's Application is `deploy/k8s/argocd/application.yaml`. It watches the `main` branch and Helm chart in this repository, and enables automated sync, pruning, and self-healing. Before applying it, replace `sha-REPLACE_WITH_COMMIT_SHA` in `deploy/k8s/helm/distributed-rate-limiter/values-gitops.yaml` with a commit tag that the successful workflow published. This Git-tracked values file also enables the CPU HPA. Make the image package public in GitHub Packages, or configure an image pull secret if it remains private. Install Argo CD in the cluster, then apply the Application manifest:

```bash
kubectl get namespace argocd >/dev/null 2>&1 || kubectl create namespace argocd
kubectl apply -n argocd --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl wait --for=condition=Available deployment --all -n argocd --timeout=10m
kubectl apply -n argocd -f deploy/k8s/argocd/application.yaml
kubectl -n argocd get applications
kubectl -n rate-limiter rollout status deployment/rate-limiter-distributed-rate-limiter
```

The Argo CD documentation recommends pinning a release for production installations; the stable manifest above is for this local learning cluster.

To demonstrate drift correction after the Application is healthy, add a temporary annotation to the live Deployment and watch Argo CD remove it. With the GitOps HPA enabled, the HPA controls replica count, so do not use `kubectl scale` for this drift test:

```bash
kubectl -n rate-limiter annotate deployment/rate-limiter-distributed-rate-limiter \
  demo-drift=temporary --overwrite
kubectl -n argocd get application distributed-rate-limiter -w
```

Capture the Application sync/health state and before/after annotation values in `phase5-results.md`. The annotation should disappear from the live Deployment after self-heal:

```bash
kubectl -n rate-limiter get deployment/rate-limiter-distributed-rate-limiter \
  -o jsonpath='{.metadata.annotations.demo-drift}'
```

Do not edit the live resource during another load or disruption experiment.

## Completion evidence

Phase 5 is complete when the repeatable load and disruption results are recorded, HPA scale-up and scale-down behavior is measured (or a clear limiting factor is recorded), both Kubernetes gateway targets are healthy in Prometheus, the dashboard shows the expected data, the CI scan result is reviewed, and Argo CD self-heal has been demonstrated. Keep results scoped to the cluster and machine used; local Kubernetes measurements are not production capacity guarantees.
