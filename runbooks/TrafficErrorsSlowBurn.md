# TrafficErrorsSlowBurn

**Severity:** warning ( phone, normal priority )

## What fired

Over the last day, more than 1.5% of a route's real requests got a 5xx
( 3x the SLO's 0.5% budget rate ), at least 10 of them, and it is still
happening in the last 2 hours. Rules: `deployments/17-traffic-slo.yaml`.

## Impact

Some visitors get error pages some of the time: not an outage, but enough
to spend a tenth of the month's budget in a day.

## Check

Which codes, from where, and when:

```
sum by (code, source) (increase(kong_http_requests_total{service="<service>", code=~"5.."}[1d]))
sum by (code) (increase(kong_http_requests_total{service="<service>", code=~"5.."}[1h]))
```

Errors bunched in time point to an event ( a rollout, a node reboot by
kured, a game day ); spread evenly they point to one path or one pod that
keeps failing. The app's logs in Loki at those times say which.

## Fix

Depends on the cause. Errors during planned node maintenance should not
reach visitors at all: if they do, the drain or the gateway handover is
the thing to fix, not the alert.

## History

- New on 2026-11-21, with Kong's metrics. Not fired yet.
