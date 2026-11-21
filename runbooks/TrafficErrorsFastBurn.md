# TrafficErrorsFastBurn

**Severity:** critical ( phone, high priority )

## What fired

Real visitors are getting 5xx answers from one route ( the `service` label,
`httproute.<namespace>.<route>.<rule>` ), counted at Kong for every request,
fast enough to use 2% of the 30-day error budget in an hour or 5% in six
hours ( SLO 99.5% non-5xx ), with at least 5 failed requests in the last
hour. Rules: `deployments/17-traffic-slo.yaml`.

The checker-based StatusSLOFastBurn asks one request every 5 minutes; this
one counts what visitors actually got, so it also catches errors on some
paths or some of the time.

## Impact

Visitors of that route get error pages now. A 502 or 503 with
`source="kong"` means Kong had no working backend; with `source="service"`
the app itself answered with the error.

## Check

The diagnostics message that follows the alert has the route's events,
logs and last rollout. Then:

```
sum by (code, source) (increase(kong_http_requests_total{service="<service>"}[15m]))
sum by (pod) (increase(kong_http_requests_total{service="<service>", code=~"5.."}[15m]))
```

- **One gateway pod only**: that gateway or its node, see
  `kubectl -n kong get pods -o wide`.
- **`source="kong"`, all gateways**: the backend pods are not ready or
  unreachable: `kubectl -n <namespace> get pods,endpointslices`.
- **`source="service"`**: the app answers with errors: its logs ( Loki,
  `{namespace="<namespace>"}` ) and its last rollout.

## Fix

Fix the cause from the check above. A rollout that broke it: roll back by
reverting the commit ( Flux applies it ); a canary of status-web rolls
back by itself. The alert resolves within minutes of the errors stopping.

## History

- New on 2026-11-21, with Kong's metrics. Not fired yet.
