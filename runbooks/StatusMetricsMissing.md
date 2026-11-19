# StatusMetricsMissing

**Severity:** critical (phone, high priority)

## What fired

For 10 minutes Prometheus has had no `status_data_readable == 1` from
status-api: either it can't scrape status-api (port 9100) at all, or
status-api can't read the checker's data file.

## Impact

The SLO alerts can't fire, whatever happens to the sites. The status page
may still work (it reads `/api/status`, not the metrics).

## Check

```
kubectl -n public-demo get pods -l app=status-api -o wide
kubectl -n public-demo get servicemonitor status-api
kubectl -n public-demo port-forward svc/status-api 9100:9100
curl -s localhost:9100/metrics | grep status_data
```

- No pods / not ready: a bad release or a node problem; `kubectl -n
  public-demo describe pod`.
- `status_data_readable 0`: the mounted ConfigMap is missing or not
  valid JSON; `kubectl -n public-demo get cm status-data`.
- Metrics fine but Prometheus doesn't have them: Prometheus targets
  (Grafana, or port-forward Prometheus and open /targets); the
  ServiceMonitor needs the `release: grafana` label.

## Fix

A bad status-api release: `git revert` the "Deploy new images" commit. A
broken ConfigMap: the next checker run rewrites it (or run one by hand,
see [StatusCheckerStale](StatusCheckerStale.md)); if it was deleted, Flux
recreates the seed (`deployments/01-status-data-configmap.yaml`).
