# DeployFreeze

**Severity:** warning ( phone, normal priority )

## What fired

A service served from the cluster ( the `service` label: Cluster dashboard
or Whoami demo ) has spent its 30-day error budget. SLO 99.5% leaves about
216 minutes of failed checks per 30 days, and they are used up.

The error budget policy in `deployments/15-deploy-freeze.yaml` acts on
this alert: within 5 minutes the `deploy-freeze` CronJob suspends Flux's
image automation ( the `raspberry-k3s` ImageUpdateAutomation ), so new
image versions are no longer written into the repo. It resumes it by
itself when the alert stops.

## Impact

None for visitors by itself. New builds of the cluster's images wait in the
registry instead of going out. Everything else Flux does keeps working:
changes committed to the repo are still applied.

## Check

Is the freeze on, and who set it:

```
kubectl -n flux-system get imageupdateautomation raspberry-k3s \
  -o jsonpath='{.spec.suspend} {.metadata.annotations}{"\n"}'
kubectl -n public-demo logs job/$(kubectl -n public-demo get jobs -l batch.kubernetes.io/cronjob-name=deploy-freeze \
  --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[-1].metadata.name}')
```

`smega.eu/frozen-by: error-budget-policy` means the policy set it. Without
the annotation, someone suspended it by hand, and the policy leaves it alone.

Where the budget went: the service's card on https://status.smega.eu ( the
red bars and their reasons ), and in Prometheus:

```
status_service_error_budget_remaining_ratio
status_service_error{service="<service>"}
```

## Fix

The freeze is the policy working, not the problem. Fix what is eating the
budget, then let the budget come back: it recovers as the failed days
leave the 30-day window, and the freeze lifts by itself.

**A release has to go out anyway** ( a hotfix, or the fix for the failures
themselves ): commit the new image tag by hand in the manifest and push.
Flux applies it like any other change; only the automatic tag updates are
frozen.

**Lifting the freeze early** is a decision, not a fix: `flux resume image
update raspberry-k3s` lifts it until the next run of the CronJob, which
suspends it again while the alert fires. To keep it off, suspend the
CronJob too ( `kubectl -n public-demo patch cronjob deploy-freeze -p
'{"spec":{"suspend":true}}'` ) and undo both afterwards.

## History

- New on 2026-11-20, with the error budget policy. Not fired yet.
