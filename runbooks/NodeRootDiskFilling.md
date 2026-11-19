# NodeRootDiskFilling

**Severity:** warning (phone, normal priority)

## What fired

A Pi's root filesystem (`instance`, the node-exporter address) has been
over 80% full for 10 minutes.

## Impact

None yet. At about 85% (counted the way node-exporter does) the kubelet
reports DiskPressure: it stops scheduling pods there and evicts some,
the node's Kong gateway among the first, so the VPS sends that node's
share of traffic elsewhere. Evicted pods start on other nodes. Above
that the SD card slows down and image pulls fail.

## Check

```
ssh root@<node> 'df -h /; du -xsh /var/lib/rancher /var/log /var/tmp /var/cache/apt /root 2>/dev/null'
kubectl describe node <node> | grep -A3 Conditions
kubectl get pods -A --field-selector=status.phase=Failed   # evicted ones
```

Usual suspects: container images (`/var/lib/rancher/k3s/agent/containerd`),
logs (`/var/log`, the journal), apt's cache, a leftover file in /var/tmp,
a local-path volume (`/var/lib/rancher/k3s/storage`) that grew.

## Fix

- Unused images: `k3s crictl rmi --prune` on the node.
- Journal: `journalctl --vacuum-size=200M`.
- apt: `apt-get clean`.
- A local-path volume: find its pod (`kubectl get pv`), shrink what it
  keeps (retention), or move it.
- Evicted pods left behind: `kubectl delete pods -A --field-selector=status.phase=Failed`.

## History

- 2026-11-19 game day (`fallocate` on kube-slave3 to 84%): DiskPressure in
  31 s, Kong gateway evicted first, 0 failed requests. No alert reached
  the phone at 93%, which is why this one exists.
