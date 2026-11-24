# StatusSLOFastBurn

**Severity:** critical (phone, high priority)

## What fired

A service on the status page fails its checks fast enough to use up a big
part of its 30-day error budget (SLO 99.5%, so 0.5% of checks may fail)
within hours: more than 14.4x the allowed rate over 1h (and still in the
last 5m), or 6x over 6h (and still in the last 30m). The `service` label
says which one.

## Impact

Probably real: visitors of that site get errors or timeouts right now.
The short window means the alert resolves on its own soon after the
service recovers.

## Check

0. Why the checks fail, kept 30 days even after the checker's 24h history
   is gone (in Grafana / Prometheus):
   ```
   status_service_error
   ```
   `ConnectionError (NameResolutionError)` is DNS, `(NewConnectionError)` a
   refused connection, `ReadTimeout` a slow answer, `HTTP 5xx` an error
   page. A check that never reached the VPS nginx (nothing in
   `/var/log/nginx/<host>.access.log` at that minute) failed on the way out
   of the cluster.
1. Open the site from outside (phone, not the home network) and
   https://status.smega.eu: one service red, or all of them?
2. **All red**: the way in is broken, not the sites. Check from the
   outside in: Cloudflare, the nginx on the VPS, Tailscale to the
   cluster, Kong:
   ```
   kubectl -n kong get pods -o wide
   kubectl get nodes
   ```
   The external probe on the VPS reports to RunGuard independently of the
   cluster; if it's green, suspect the checker instead (below).
3. **One red**: is the service in the cluster?
   ```
   kubectl get pods -A -o wide | grep -v Running
   kubectl get httproute -A
   ```
   For a site hosted elsewhere, log in there.
4. **The checker itself**: what did the last runs see?
   ```
   kubectl -n public-demo get jobs --sort-by=.metadata.creationTimestamp | tail -3
   kubectl -n public-demo logs job/<newest>
   ```
   A timeout after 8s on a slow but working site counts as a failure.

## Fix

Bring the service back. If it was a bad release, `git revert` the "Deploy
new images" commit (status-web rolls back by itself, see
[CanaryRolledBack](CanaryRolledBack.md)). If a node is down, the apps run
two replicas on two Pis and should have moved; check the PDBs and
`kubectl get pods -A -o wide`.

Afterwards: an entry in the incident log (`content/incidents.json`), and
a postmortem in `postmortems/` if it lasted or taught something.

## History

- 2026-11-14: the master reboot left Kong without routes (404 on every
  site) until a controller pushed them; now 2 controllers.
- 2026-11-24: one failed check ( a 04:00 timeout on the cluster dashboard, the home connection's nightly blip ) paged as critical: one check holds the gauge at 0 for 5 of 60 minutes, 8.3%, over the 1-hour threshold. The 1-hour condition now also needs at least 2 failed checks in that hour ( status:failed_checks:1h ).
