# NodeCordonedTooLong

**Severity:** warning (phone, normal priority)

## What fired

A node (`node`) has been cordoned (unschedulable) for more than 2 hours.
Planned maintenance cordons a node for under an hour: a kured reboot
takes minutes, an OS upgrade run 30-55 minutes, a k3s upgrade less.

## Impact

None for visitors by itself: the public sites run with 2 replicas spread
over the nodes. But while any node is cordoned, `NodeMaintenance` fires
and Alertmanager inhibits the node and pod alerts everywhere
(`monitoring/maintenance-rules.yaml`), so a real problem elsewhere stays
quiet. kured also won't reboot another node meanwhile.

## Check

```
kubectl get nodes
kubectl describe node <node> | grep -iE "taints|unschedulable|kured|upgrade"
kubectl -n kube-system logs ds/kured --since=3h | grep -i <node>
kubectl -n system-upgrade get plans,jobs
```

- kured annotations or a kured log line: a reboot that didn't finish
  (the node never came back, or the uncordon failed).
- A system-upgrade job for the node: a k3s upgrade stuck on it.
- Neither: a hand-made `kubectl cordon` or an os-upgrade script that
  stopped half way (its log on the laptop; the node may also still have
  the `smega.eu/maintenance` label, which keeps the Kong gateway off it).

## Fix

If the node is healthy (Ready, `ssh` works, `uname -r` is what's
expected):

```
kubectl uncordon <node>
kubectl label node <node> smega.eu/maintenance-   # if it's there
```

If it isn't: it stays cordoned while you fix it, but then the inhibition
is hiding alerts. Look at the inhibited ones directly in Alertmanager
(`kubectl -n monitoring port-forward svc/grafana-kube-prometheus-st-alertmanager 9093`,
"Inhibited" filter) or in Prometheus: `ALERTS{alertstate="firing"}`.

## History

- New on 2026-11-19, with the maintenance inhibition. Planned windows so
  far: OS upgrades 30-55 min, the longest cordon 2 h 26 min (the
  kube-slave3 crossgrade repair, 2026-11-16).
