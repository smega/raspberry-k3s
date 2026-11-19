# StatusSLOSlowBurn

**Severity:** warning (phone, normal priority)

## What fired

A service has failed often enough over the last days to run out of its
30-day error budget (SLO 99.5%) before the window ends: 3x the allowed
rate over 1 day (still in the last 2h), or 1x over 3 days (still in the
last 6h). `service` says which one.

## Impact

Nothing dramatic right now; this is the slow kind: occasional failures,
a few minutes a day. Left alone, the service ends the month below its SLO.

## Check

1. The service's row on https://status.smega.eu: the 24h bar shows when
   the failures were. A pattern (same time every night) points at a job:
   kured reboots (Mon/Tue/Thu 03:00-06:00), k3s upgrades (Wed 02:30-06:00),
   backups.
2. What the failures were:
   ```
   kubectl -n public-demo get jobs --sort-by=.metadata.creationTimestamp | tail -3
   kubectl -n public-demo logs job/<newest>
   ```
   Timeouts (8s) vs HTTP errors vs connection refused.
3. In Grafana, `status:error_ratio:avg1d{service="<name>"}` over the last
   week: getting worse or a one-off?

## Fix

Depends on the cause. Failures during reboots: is the app running 2
replicas with a PDB (`deployments/13-disruption-budgets.yaml`)? A site that
answers slowly: the checker's 8s timeout is the line. A flaky site hosted
elsewhere: fix it there; the cluster isn't involved.

## History

- 2026-11-14/15: node drains caused short outages while apps had one
  replica; now 2 replicas, PDBs, preStop and Kong timeouts (the game days
  in the infra repo).
