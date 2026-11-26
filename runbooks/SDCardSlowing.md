# SDCardSlowing, SDCardVerySlow, SDCardWearHigh

**Severity:** warning (phone, normal priority)

Three alerts on the same card, from the same probe:

- **SDCardSlowing:** slower than its own past ( below ). Needs a week of
  history, so it is blind on a new card.
- **SDCardVerySlow:** a day's median 4 KB fsynced write over 100 ms, no
  history needed. Healthy cards here: 7-35 ms.
- **SDCardWearHigh:** written over 500 times its size, from ext4's own
  count of what was written to the root filesystem
  ( `sd_card_rootfs_written_bytes`, `sd_card:full_writes` ). A floor: the
  card writes more internally. Not a failure, a "plan the next card".

## What fired

A node's SD card got slow at small synchronous writes: for a whole day,
the median 4 KB write with fsync took 2.5 times as long as over the two
weeks before. The probe is `monitoring/sd-card-health.yaml`: every 6
hours, the same fixed job on every card ( 32 MB written and read back,
50 small fsynced writes ), so a busy day doesn't look like a worn card.

## Impact

Nothing visible yet. Small synced writes are what k3s's datastore,
Loki's WAL and Prometheus' WAL do all the time, so a slow card makes the
node slow first: probes time out, pods restart, image pulls and Trivy
scans saturate it ( as on kube-slave2, whose card is from 2016 ). The
next steps are kernel errors ( SDCardKernelErrors ) and a read-only root
filesystem ( NodeRootFilesystemReadOnly ).

## Check

The trend, per node ( Prometheus or Grafana Explore ):

```
sd_card_probe_sync_write_seconds
sd_card_probe_write_seconds
sd_card_probe_read_seconds
sd_card_info
```

- Only small writes slower, sequential the same: the card's internal
  garbage collection is struggling, typical of a nearly worn card.
- Everything slower at once, on every node: not the cards; a new
  workload writing a lot ( `rate(node_disk_written_bytes_total[1h])` ).
- `ssh root@<node> dmesg | grep -iE "mmc|I/O error"`: any errors make it
  urgent.

## Fix

Replace the card while it still reads, before it fails:

1. A new high-endurance card, cloned from the old one ( `dd` from
   another machine, or `rpi-clone` on the node ), or a fresh golden
   image ( smega/pi-golden-image ) and a rejoin.
2. `kubectl drain <node> --ignore-daemonsets --delete-emptydir-data`,
   swap the card, `kubectl uncordon <node>`.

The card's own baseline restarts with the new card: the alert needs a
week of history before it can fire again.

## History

- New on 2026-11-23. First baselines: kube-slave2 is the slow one
  ( SanDisk SL32G from 2016 ), already on the TODO for a new card.
- 2026-11-25/26: that card went from 8-17 ms to 700-2400 ms per write.
  SDCardSlowing stayed quiet ( three days of history, it wants seven ),
  while the node failed a CronJob 39 times, took 3-5 minutes per pod
  start and made two canaries fail. Card replaced by a clone the same day
  ( 6.3 ms per write ). SDCardVerySlow, NodeIOWaitHigh and PodStartSlow
  were added after it; backtested, they would have fired from the evening
  before ( iowait ) and early that morning ( very slow ). SDCardWearHigh
  and the wear numbers came with them: kube-slave1 ~125 full writes,
  kube-master ~97, kube-slave3 ~17.
