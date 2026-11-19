# StatusCheckerStale

**Severity:** critical (phone, high priority)

## What fired

The status checker (CronJob `public-demo/status-checker`, every 5 minutes)
has not written new results for over 15 minutes.

## Impact

The status page shows old data, and the SLO alerts are blind: they
read the checker's results, which are frozen at their last value. The
sites themselves may be fine.

## Check

```
kubectl -n public-demo get cronjob status-checker
kubectl -n public-demo get jobs --sort-by=.metadata.creationTimestamp | tail -3
kubectl -n public-demo get pods -l job-name --sort-by=.metadata.creationTimestamp | tail -3
kubectl -n public-demo logs job/<newest>
```

- `SUSPEND True`: someone (or something) suspended it.
- Jobs not created at all: the cronjob controller (k3s server) is down or
  the master is unhealthy: `kubectl get nodes`.
- Pods `ImagePullBackOff`: the registry (zot) or the image tag. A pod the
  policy-controller rejects (unsigned image) shows in the Job's events.
- Pods failing: the log says why; a failed write to the ConfigMap
  (`status-data`) is usually RBAC (`deployments/02-status-checker-rbac.yaml`).

## Fix

Fix the cause, then run it once by hand instead of waiting:

```
kubectl -n public-demo create job --from=cronjob/status-checker status-checker-manual-$(date +%s)
```

The alert resolves within a few minutes of a successful run (status-api
reads the ConfigMap through a volume, which the kubelet refreshes about a
minute later).
