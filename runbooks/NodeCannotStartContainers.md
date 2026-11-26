# NodeCannotStartContainers, NodeIOWaitHigh, PodStartSlow

**Severity:** warning (phone, normal priority)

The two earlier signs of the same problem, before containers fail
outright ( `monitoring/sd-card-health.yaml` ):

- **NodeIOWaitHigh:** the node's CPUs wait on the disk over 15% of the
  time for an hour. Healthy nodes here stay under 15%.
- **PodStartSlow:** the kubelet's pod start p90 ( creation to running,
  image pull not counted ) over 2 minutes for 30 minutes, with at least 3
  starts in the hour.

Both point at the node's SD card first: compare its
`sd_card_probe_sync_write_seconds` with the other nodes'.

## What fired

kubelet on a node counted over 10 failed container or pod sandbox
creations in 10 minutes, for 10 minutes ( `kubelet_runtime_operations_errors_total`,
`monitoring/node-runtime-rules.yaml` ). Its container runtime ( k3s's
containerd ) isn't starting anything.

## Impact

The node can stay Ready, so Kubernetes doesn't move its pods: every pod
there is down, and anything that restarts or gets scheduled there stays
down. Public sites survive on their other replica; single-replica pods
on that node ( kube-state-metrics was one ) are simply gone.

## Check

```
kubectl get pods -A -o wide --field-selector spec.nodeName=<node> | grep -v Running
kubectl -n <ns> describe pod <pod> | tail -15
ssh root@<node> 'uptime; cat /sys/block/mmcblk0/stat; dmesg -T | grep -iE "mmc|I/O error" | tail'
ssh root@<node> 'tail -n 200 /var/lib/rancher/k3s/agent/containerd/containerd.log | grep level=error'
```

- "failed to reserve container name" and "context deadline exceeded":
  containerd is too slow to finish a create before kubelet gives up, and
  every retry leaves a name reserved. Usually the disk: check the SD card
  probe ( `sd_card_probe_sync_write_seconds` ) and whether the card is
  close to 100% busy ( two `/sys/block/mmcblk0/stat` readings 5 s apart,
  10th field ).
- Image pull errors: a different problem ( registry, credentials ).

## Fix

Restarting k3s-agent alone didn't help on 2026-11-23. Drain and reboot:

```
kubectl drain <node> --ignore-daemonsets --delete-emptydir-data --timeout=180s
ssh root@<node> reboot
```

Measure the card after boot ( 50 fsynced 4 KB writes, see the SD card
probe ) and uncordon only if it's back to its usual speed. If it isn't,
leave it cordoned and replace the card.

## History

- New on 2026-11-23, after kube-slave2 stayed in this state for 1.5 hours
  following the CoreDNS node-crash game day: ~340 ms per write on its 2016
  SD card, up to 455 failed creates per 10 minutes. After the reboot: 8 ms.
- 2026-11-26: the same card, worn further. iowait 19-36% from the day
  before, pod starts up to 276 s ( p90 ), no failed creates, so this alert
  stayed quiet. NodeIOWaitHigh and PodStartSlow were added; backtested,
  iowait would have fired a day before the card was replaced.
