# CanaryStuck

**Severity:** warning (phone, normal priority)

## What fired

A canary release (`name`, in `namespace`) has been in progress for 20
minutes. A normal one takes about 4.

## Impact

Up to 50% of the visitors may be on the new version, which hasn't
been promoted yet, and nothing else gets released until this one ends.

## Check

```
kubectl -n public-demo get canary status-web
kubectl -n public-demo describe canary status-web | tail -20
kubectl -n flagger-system get pods
kubectl -n flagger-system logs deploy/flagger --since=30m | grep status-web | tail
```

- Flagger not running: nothing moves the release forward or back; the
  traffic split stays where it was. Start there.
- Repeated `New revision detected` / restarted analysis: the Deployment
  keeps changing under it (a burst of image updates, or Flux and something
  else fighting over the template; `kubectl -n public-demo get rs` shows
  many fresh ReplicaSets).
- Waiting on the Deployment: canary pods pending (no room on the nodes) or
  not ready; `kubectl -n public-demo get pods -l app=status-web`.

## Fix

Once Flagger runs again it carries on by itself. To end the release now:

- **Back to the old version**: revert the commit that started it. Flagger
  restarts the analysis on the reverted Deployment, and that one passes.
- **Promote without the checks** (only if you know the new version is
  good):
  ```
  kubectl -n public-demo patch canary status-web --type merge -p '{"spec":{"skipAnalysis":true}}'
  ```
  and set it back to `false` once promoted: Flux doesn't own that field,
  so it would stay on and every later release would skip its checks.
