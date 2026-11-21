# TrafficLatencySLOBurn

**Severity:** warning ( phone, normal priority )

## What fired

More than 6% of the status page's requests over 6 hours ( or 3% over a
day ) were slower than the target, at least 10 of them in the day. SLO:
99% of requests under 250 ms for the page ( `status-web` ) and under 2 s
for its API ( `status-api` ), measured at Kong from the request's arrival
to the last byte. Rules: `deployments/17-traffic-slo.yaml`.

## Impact

The page still works, slowly. On the API a slow request is usually a
cold `/api/dora` ( it pages through GitHub ) or Prometheus answering
slowly.

## Check

Where the time goes, Kong's own share against the backend's:

```
histogram_quantile(0.95, sum by (le) (rate(kong_request_latency_ms_bucket{service="<service>"}[30m])))
histogram_quantile(0.95, sum by (le) (rate(kong_upstream_latency_ms_bucket{service="<service>"}[30m])))
```

- **Upstream slow**: the app. CPU throttling
  ( `tools/resource-report.py` ), its logs and traces ( Tempo ).
- **Kong slow, upstream fast**: the gateway or its node: CPU, memory,
  `kubectl top pods -n kong`.

## Fix

Throttling: raise the CPU limit ( the load test showed limits are what
caps throughput ). A slow dependency: Prometheus load, GitHub's API. If
the target itself is wrong for the page, change it in the rule with a
note why, not quietly.

## History

- New on 2026-11-21, with Kong's metrics. Not fired yet.
