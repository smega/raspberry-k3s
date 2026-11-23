# ClusterDNSDown, ClusterDNSDegraded, ExternalDNSFailing

**Severity:** ClusterDNSDown critical; the others warning

## What fired

The blackbox exporter resolves two names through the kube-dns Service
( 10.43.0.10 ) every 30 seconds ( `monitoring/dns-probe.yaml` ):

- **ClusterDNSDown:** `kubernetes.default.svc.cluster.local` failed every
  time for 3 minutes. CoreDNS is down.
- **ClusterDNSDegraded:** under 90% of those lookups worked over 10
  minutes. One replica, or its node, is in trouble.
- **ExternalDNSFailing:** `github.com` failed more often than not for 5
  minutes while cluster names work: the upstream ( router, ISP ).

## Impact

Down: every new connection that starts with a name fails, inside and
out. Running connections keep working, so the public pages may still
answer for a while; the checker, Flux, image pulls and the dashboard's
database connection don't.

External only: Flux can't fetch git, images can't be pulled, the
checker reports outside services as down.

## Check

```
kubectl -n kube-system get pods -l k8s-app=kube-dns -o wide
kubectl -n kube-system get endpointslices -l kubernetes.io/service-name=kube-dns
kubectl -n kube-system logs -l k8s-app=kube-dns --since=15m | tail -30
kubectl get nodes; kubectl top nodes
```

- A replica not ready, or on a node that's NotReady or saturated
  ( load, `NodeDiskIOSaturation` ): that's the cause. On 2026-11-22 it
  was a Trivy database unpack on the same node.
- Both replicas fine but lookups fail: the CoreDNS ConfigMap
  ( `kubectl -n kube-system get cm coredns -o yaml` ) or kube-proxy's
  rules on the nodes.
- External only: `ssh root@kube-slave1 'dig github.com @192.168.2.1'` and
  against 1.1.1.1. The router or the ISP; not ours to fix, but if it
  lasts, CoreDNS can forward to 1.1.1.1 instead.

## Fix

- A replica down: `kubectl -n kube-system delete pod <pod>`; it comes
  back on a node the topology spread allows. The HPA keeps two
  ( `deployments/19-coredns.yaml` ).
- Its node saturated: find what saturates it ( `iotop`, the Trivy
  server ) and move that.
- Both gone: k3s re-applies CoreDNS on every start:
  `ssh root@kube-master systemctl restart k3s`.

## History

- New on 2026-11-23, after the 2026-11-22 outage ( ~15 minutes, one
  CoreDNS replica starved by a Trivy scan on its node ). CoreDNS has had
  two replicas since the same day.
- Known noise source: a ~30 s upstream failure at about 04:00 UTC on some
  nights ( 2026-11-21, 11-23 ), too short for ExternalDNSFailing.
