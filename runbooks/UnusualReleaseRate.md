# UnusualReleaseRate

**Severity:** warning (phone, normal priority)

## What fired

A Deployment (`namespace`/`owner_name`) got more than 5 new ReplicaSets
in an hour, for 30 minutes. Every rollout makes one, so this counts
releases, whoever started them. A normal busy hour has 1-4. The count
stops at 11 ( `revisionHistoryLimit` + 1 ): older ReplicaSets are deleted.

## Impact

Usually none that visitors see: each rollout can succeed. But pods are
replaced over and over, the API server and etcd do the work, and a real
release may never finish ( a canary restarts its analysis on every new
revision ). In the 2026-11-21 case the live API also served data up to
11 minutes old.

## Check

What changes between two ReplicaSets:

```
kubectl -n <ns> get rs -l app=<app> --sort-by=.metadata.creationTimestamp
kubectl -n <ns> rollout history deploy/<owner_name>
diff <(kubectl -n <ns> get rs <older> -o jsonpath='{.spec.template}' | jq .) \
     <(kubectl -n <ns> get rs <newer> -o jsonpath='{.spec.template}' | jq .)
```

Then who is changing it:

- **Flagger** ( `*-primary` Deployments, or the canary target ):
  `kubectl -n flagger-system logs deploy/flagger --since=1h | grep -i <app>`.
  "configmap X has changed" or "secret X has changed" means it tracks
  something that isn't configuration.
- **Flux:** `flux get kustomizations` and `flux events`; two sources
  fighting over one field ( Flux against an HPA, a mutating webhook, a
  hand edit ) shows up as a diff that flips back and forth.
- **Image automation:** `flux get images all -A`, a tag policy that
  keeps picking a different tag.

## Fix

Stop the loop at its source:

- Flagger and a ConfigMap or Secret that holds data:
  `flagger.app/config-tracking: disabled` on it ( see
  `deployments/01-status-data-configmap.yaml` ). If Flux only creates the
  object ( `ssa: IfNotPresent` ), set it on the live object too.
- Two writers on one field: give the field to one of them ( drop it
  from git, or add it to the HelmRelease's / Kustomization's ignore ).
- To stop it right away while you look: `flux suspend kustomization <name>`
  or `kubectl -n <ns> patch canary <app> --type merge -p '{"spec":{"suspend":true}}'`.

## History

- New on 2026-11-23, after Flagger re-released status-api every 5
  minutes for 16 hours ( 2026-11-21 18:40 to 11-22 10:50 ) and nothing
  fired.
