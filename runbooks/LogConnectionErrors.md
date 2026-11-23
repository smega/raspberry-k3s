# LogConnectionErrors

**Severity:** warning (phone, normal priority)

## What fired

A public app's pod logged over 20 refused or timed-out connections per
15 minutes, for 15 minutes ( Loki's ruler,
`observability/loki-rules.yaml` ). Something it depends on is
unreachable: its backend, a database, an outside API.

## Impact

The page itself may still answer, which is why no availability SLO
fires. On 2026-11-22 the dashboard served its page for four hours while
every request to its backend failed.

## Check

What it can't reach:

```
{namespace="<ns>", pod="<pod>"} |~ "(?i)connection refused|i/o timeout|ETIMEDOUT|deadline"
kubectl -n <ns> get pods,endpoints
kubectl -n <ns> get networkpolicy
```

- The target's pods are down or not ready: fix those first.
- The target is up: a NetworkPolicy. Egress is default deny in the
  public namespaces ( `deployments/16-network-policies.yaml` ). A missing
  egress rule only shows once the old connections close, so it can
  start hours after the policy change.
- The target is outside the cluster ( MongoDB Atlas, GitHub ): their
  status page, and DNS.

## Fix

Bring back the dependency, or add the egress rule ( with the port ) and
restart the pod so new connections test it.

## History

- New on 2026-11-23, after the 2026-11-22 dashboard outage ( Atlas on
  27017 missing from the backend's egress ), which would have fired it at
  about 11:15, three and a half hours before it was noticed.
