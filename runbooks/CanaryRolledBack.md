# CanaryRolledBack

**Severity:** warning (phone, normal priority)

## What fired

Flagger stopped a release: the new version (`name`, in `namespace`) failed
its checks three times and is scaled back to zero. See "Canary releases"
in the repo README for how a release goes.

## Impact

None for visitors: the previous version (`<name>-primary`) never stopped
serving, and the new one got at most the canary share of the traffic
(10-50%), or none if it failed before the first step. But the failed
image is still in git, and the next change to the Deployment tries it
again.

## Check

Which check failed, and when:

```
kubectl -n public-demo describe canary status-web | tail -20
kubectl -n flagger-system logs deploy/flagger | grep status-web | tail -20
```

- `pre-rollout check smoke test before traffic failed` / `smoke test`
  failed: the page, its JS bundle or `/content/incidents.json` didn't
  come back right from the canary. Run the same command by hand (it's in
  `deployments/14-status-web-canary.yaml`, against
  `status-web-canary.public-demo`) from the load tester:
  ```
  kubectl -n flagger-system exec deploy/loadtester -- curl -s http://status-web-canary.public-demo/ | head
  ```
  The canary is scaled to zero after the rollback; to look at it again,
  push the same image once more or reproduce locally (`npm run build`,
  `npm run preview` in `frontend/`).
- `no restarts` above 0: the new container crashed; its logs are gone with
  the pod, so reproduce it locally.
- `progress deadline exceeded`: the canary pods never became ready
  (image pull, the policy-controller rejecting an unsigned image, crash
  loop). `kubectl -n public-demo get events --sort-by=.lastTimestamp`.

## Fix

Fix forward (a new commit, a new build, a new canary) or revert the
commit that broke it. Don't revert the "Deploy new images" commit alone:
image automation puts the newest tag straight back.

The alert clears when the next release starts.

## History

- 2026-11-19: game day with a deliberately broken build (root element
  renamed): rolled back at the pre-rollout smoke test, no request reached
  it.
