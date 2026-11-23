# SDCardKernelErrors, NodeRootFilesystemReadOnly, SDCardProbeMissing

**Severity:** warning; NodeRootFilesystemReadOnly critical

## What fired

- **SDCardKernelErrors:** the kernel logged new mmc ( SD card ) or I/O
  errors since the last probe round ( `monitoring/sd-card-health.yaml`
  counts them every 6 hours ).
- **NodeRootFilesystemReadOnly:** ext4 hit an error it couldn't recover
  from and remounted `/` read-only ( node-exporter ).
- **SDCardProbeMissing:** the probe's last round failed, or it hasn't
  finished one for 13 hours. A failed write is itself a symptom.

## Impact

Kernel errors: the card is failing; the next one may be data loss.
Read-only root: nothing on the node can write. kubelet, k3s and every
pod there fail; the node goes NotReady soon. The public sites run with 2
replicas and survive one node.

## Check

```
ssh root@<node> 'dmesg -T | grep -iE "mmc|I/O error|EXT4-fs" | tail -30'
ssh root@<node> 'findmnt / ; touch /root/.rw-test && rm /root/.rw-test'
kubectl -n monitoring logs ds/sd-card-health -c probe --since=7h
```

- One timeout line after an undervoltage or a hot spell, nothing since:
  watch it.
- Repeated errors, or a read-only root: the card is dying.

## Fix

1. `kubectl drain <node> --ignore-daemonsets --delete-emptydir-data`
   ( it may hang on a read-only node: `--disable-eviction --force` ).
2. Power off, replace the card: clone the old one if it still reads,
   otherwise a fresh image ( smega/pi-golden-image ) and rejoin.
3. `kubectl uncordon <node>`.

For SDCardProbeMissing with a healthy card: the pod's log says which
step failed; `kubectl -n monitoring rollout restart ds/sd-card-health`.

## History

- New on 2026-11-23.
