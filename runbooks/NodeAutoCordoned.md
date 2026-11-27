# NodeAutoCordoned, NodeProblemNotRemediated, NodeDrainStuck, NodeRemediatorDown

**Severity:** warning (phone, normal priority)

Four alerts on the same mechanism ( `node-health/` ):

- **NodeAutoCordoned:** node-remediator cordoned and drained a worker,
  because Node Problem Detector set its `SDCardSlow` condition.
- **NodeProblemNotRemediated:** `SDCardSlow` has been True for 30
  minutes and nothing was moved.
- **NodeDrainStuck:** pods are still on a node 30 minutes after the
  remediator cordoned it.
- **NodeRemediatorDown:** the remediator hasn't finished a round in 15
  minutes. A sick node would stay in service.

## How it works

Node Problem Detector runs on every node ( DaemonSet, kube-system ). Every
5 minutes it times 10 small writes, each with its own fsync, on the node's
SD card. Slow means 4 rounds in a row averaged over 100 ms ( 20 minutes ).
Healthy again means 4 rounds under 50 ms. Healthy cards here take 1-40 ms;
the card that failed on 2026-11-26 took 700-2400 ms. A write that fails
counts as slow, so a root filesystem gone read-only trips it too.

node-remediator ( monitoring, on kube-master ) reads node conditions every
30 seconds. Once a worker has been `SDCardSlow` for 2 minutes, it cordons
the worker and evicts its pods, with the same rules as `kubectl drain`
plus a few of its own:

- one node at a time, and only if every other node is Ready and
  schedulable;
- never the control plane;
- never if fewer than 2 healthy workers would be left;
- pods whose volume is on that node stay: Loki, Grafana, Alertmanager,
  Tempo ( local-path ) and Prometheus ( local PV ). Evicted, they would wait
  in Pending until the node came back. They run slowly instead;
- PodDisruptionBudgets are honoured, and a refused eviction is retried
  every round;
- after 15 healthy minutes it uncordons the node, and doesn't cordon it
  again automatically for 6 hours. A card on the edge would otherwise move
  every pod back and forth all day, so after that it is a human's call
  ( NodeProblemNotRemediated );
- a node uncordoned by hand stays uncordoned: it only drops its own
  annotation.

What it did is on the node: the annotations `smega.eu/cordoned-by`,
`smega.eu/cordoned-reason`, `smega.eu/cordoned-at` and
`smega.eu/auto-uncordoned-at`, and Events with the reasons `AutoCordoned` and
`AutoUncordoned`.

## Impact

None for visitors, if it works: the public pages run 2 replicas on
different nodes, and their PDBs keep 1. The pinned pods on the sick node
( Loki, Grafana, an Alertmanager replica, Tempo ) keep running slowly.

The cordon also fires NodeMaintenance, which mutes node and pod noise
while it lasts. NodeCordonedTooLong fires after 2 hours: by then a
cordon that hasn't healed is a card to replace.

## Check

```
kubectl get nodes
kubectl describe node <node> | grep -A3 SDCardSlow
kubectl get node <node> -o jsonpath='{.metadata.annotations}' | tr , '\n' | grep smega.eu
kubectl -n monitoring logs deploy/node-remediator --tail=50
kubectl -n kube-system logs -l app=node-problem-detector --field-selector spec.nodeName=<node> 2>/dev/null | tail
ssh root@<node> 'cat /var/lib/node-problem-detector/sd-history'
```

The message on the condition has the last four rounds, in ms. Compare
with the 6-hourly probe: `sd_card_probe_sync_write_seconds`
( SDCardSlowing.md ).

- **NotRemediated:** `node_remediator_blocked{node="<node>"}` says why:
  control plane, another node cordoned, too few healthy workers, the
  cooldown, or `smega.eu/auto-remediation=off`.
- **DrainStuck:** `kubectl get pdb -A`; a PDB with 0 allowed disruptions
  means its other replica isn't Ready somewhere else.
- **Down:** the pod on kube-master; if kube-master is down, so is the API,
  and nothing can be moved anyway.

## Fix

A slow card doesn't recover by itself for long. On 2026-11-26 the fix was
a new card, cloned from the old one:
`infra/projects/raspberry-k3s/ops/kube-slave2-sd-swap-2026-11-26.md`.
Once the card is new, the condition clears in about 20 minutes, and the
node is uncordoned 15 minutes after that.

To keep the remediator off a node ( maintenance by hand ):

```
kubectl annotate node <node> smega.eu/auto-remediation=off
kubectl annotate node <node> smega.eu/auto-remediation-   # back on
```

To make it let go of a node it holds, uncordon it yourself: it drops its
annotation and leaves the node alone for 6 hours.

## Test

`infra/projects/raspberry-k3s/game-days/node-remediation.sh`: a forced
"slow" on one worker ( `touch /var/lib/node-problem-detector/force-slow` ),
probes on every public site while it is drained, then the file is removed
and it waits for the automatic uncordon.

## History

- 2026-11-23 and 2026-11-26: the two incidents this was built for, both
  drained by hand ( incident log on status.smega.eu ).
