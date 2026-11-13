# Raspberry Pi k3s lab

A small, public-safe demo running on a home Raspberry Pi k3s cluster. Live at:

- [status.smega.eu](https://status.smega.eu) -- a public status page for the
  small services I run myself, checked every 5 minutes by a Kubernetes
  CronJob on this same cluster, plus live CPU temperature, CPU and memory
  usage of each Pi in the cluster.
- [whoami.smega.eu](https://whoami.smega.eu) -- a minimal ingress/routing demo
  ([traefik/whoami](https://github.com/traefik/whoami)).
- [cluster.smega.eu](https://cluster.smega.eu) -- a readonly dashboard showing
  selected live cluster state (pods, services, deployments, nodes, etc), also
  open source: [cluster-dashboard](https://github.com/smega/cluster-dashboard).
- `lab.smega.eu` / `api-lab.smega.eu` are reserved, not currently routed to
  anything.

## Layout

- `frontend/` -- Preact status page served at status.smega.eu.
- `backend/` -- Go/Gin API serving `frontend/`'s data (api-status.smega.eu). A
  thin read-only layer: it just serves whatever JSON the checker last wrote.
  `/api/nodes` adds live per-node stats (CPU temperature, CPU, memory, load,
  uptime, Ready/reachable state, and 24h temperature/CPU/memory history)
  from the cluster's own Prometheus (kube-prometheus-stack + node-exporter
  + kube-state-metrics, `PROMETHEUS_URL`). The node list comes from
  kube-state-metrics, so a Pi that is down still shows up, marked
  unreachable. It only runs
  fixed queries, caches the answer for 15s, and labels nodes as
  "Pi #N - role" instead of exposing hostnames or IPs.
  `/badge/<service>.svg` serves a shields-style uptime badge per service
  (slug of its name, e.g. `personal-site`), for READMEs.
  On a separate, unrouted port (9100) it also exposes the checker's results
  as Prometheus metrics (`status_service_up`, latency, data age), which feed
  the SLO burn-rate alerts in `deployments/11-status-slo-alerts.yaml`.
- `scripts/publish-status.sh` -- builds and pushes the status page images
  (arm64); bakes the current commit into status-web for its "Last deploy"
  line.
- `cron/` -- a Python CronJob (`check_status.py`) that checks each configured
  service every 5 minutes, keeps a rolling 24h history per service, and
  patches the result into a ConfigMap.
- `pi-temp-led/` -- a DaemonSet that drives an RGB LED on every Pi from its
  own CPU load (blue when idle, red when busy), plus its own manifests and
  README. The status page's per-node dot uses the same color scale.
- `kong/` -- Helm values for installing Kong as the cluster's Gateway API
  implementation.
- `deployments/` -- the rest of the Kubernetes manifests (namespaces, RBAC,
  the apps above, the Gateway/HTTPRoutes, and the readonly dashboard),
  applied roughly in filename order.

## Readonly dashboard: what it can and can't see

`public-dashboard-sa` (the dashboard backend's service account) is granted a
`ClusterRole` with `get/list/watch` on a fixed set of resource types (pods,
services, deployments, nodes, jobs, RBAC objects, etc) -- deliberately
**excluding Secrets**. A public readonly dashboard has no legitimate reason to
read Secret contents, and since this is a ClusterRole (not namespace-scoped),
including it would let the dashboard's own token read every Secret on the
cluster. See `deployments/09-dashboard-readonly-rbac.yaml`.

## Apply order

```sh
kubectl apply -f deployments/00-namespaces.yaml
kubectl apply -f deployments/01-status-data-configmap.yaml
kubectl apply -f deployments/02-status-checker-rbac.yaml
kubectl apply -f deployments/03-status-api.yaml
kubectl apply -f deployments/04-status-web.yaml
kubectl apply -f deployments/05-status-checker-cronjob.yaml
kubectl apply -f deployments/06-whoami.yaml
kubectl apply -f deployments/07-gateway.yaml
kubectl apply -f deployments/08-httproutes.yaml
kubectl apply -f deployments/09-dashboard-readonly-rbac.yaml
kubectl apply -f deployments/10-dashboard.yaml
kubectl apply -f deployments/11-status-slo-alerts.yaml
```

`11-status-slo-alerts.yaml` needs kube-prometheus-stack (the
ServiceMonitor/PrometheusRule CRDs, and a Prometheus selecting
`release: grafana` - change the label to your release name). It alerts on
the page's own SLO (99.5% over 30 days) with multi-window, multi-burn-rate
rules, and on the checker itself going stale.

`deployments/07-gateway.yaml` needs `gatewayClassName` set to whatever Gateway
API implementation you're running (`kong`, if you're using the Helm chart in
`kong/kong-values.yml`), and lives in the `kong` namespace -- the namespace
the Kong Helm release itself creates, not one of the namespaces in
`00-namespaces.yaml`. The dashboard also needs a `dashboard-secrets` Secret
(`mongo_uri`, `jwt_secret`) created separately -- never commit that Secret's
values here.

By default the Gateway only accepts HTTPRoutes from namespaces labelled
`expose-via-public-gateway: "true"` (see `00-namespaces.yaml`) -- so adding a
new public app means labelling its namespace, not touching the Gateway.

## Configuring which services the status page checks

`cron/check_status.py` ships with a default `name -> url` map. Override it
without rebuilding the image by setting `STATUS_TARGETS_JSON` on the
CronJob's container to a JSON object of the same shape.
