# 2026-11-17: every node config backup of three days was unreadable

| | |
|---|---|
| Affected | the per-node config backups taken before each OS upgrade, 2026-11-15 to 2026-11-17 (7 tarballs, 4 nodes) |
| Not affected | the k3s datastore backups (6-hourly, restore-tested weekly) |
| Impact on visitors | none |
| Real cost | none: no upgrade needed a restore |
| Detected by | opening one while writing a "replace a node's SD card" runbook |
| Status | resolved; new backups checked on the backup host, restore tested |

## Summary

Before every in-place OS upgrade (Debian bookworm -> trixie, then 32-bit
-> 64-bit userland) the upgrade script streamed a tarball of each node's
configuration (/etc, /boot/firmware, /root, /usr/local, Tailscale state,
crontabs) over ssh to another machine. Every one of these tarballs was
corrupt: they stopped after 0-700 of the ~3,100 files. Nobody had tried
to open one until 2026-11-17, when a runbook for rebuilding a node needed
a file from one. The upgrades all went well, so the backups were never
needed, but for three days the "backup" step in the script had been a
placebo.

## Impact

- 7 backups unusable: three workers before trixie and before arm64, the
  control-plane node before trixie.
- If an upgrade had broken a node badly enough to need a reinstall, its
  configuration (k3s agent settings, Tailscale identity, ssh keys, cron
  jobs) would have had to be rebuilt from memory and the repo.
- The k3s datastore backups use a different path and were fine, so the
  cluster itself was always recoverable.

## Timeline (UTC)

| Time | Event |
|---|---|
| 2026-11-15 18:05 | First backup through the new upgrade script (kube-slave3, before trixie). Corrupt, unnoticed |
| 2026-11-15 to 11-17 | Six more, one before each node's trixie and arm64 upgrade. All corrupt |
| 2026-11-17, before 14:00 | Writing the SD card replacement runbook: `tar -tzf` on a backup to show what's in it. It stops with "unexpected end of file". All 7 checked: every one breaks early. Cause found, backup function rewritten |
| 14:01-14:16 | Fresh backups of all 4 nodes, each checked on the backup host; a single-file restore tested |
| 15:28 | The 7 broken tarballs deleted |
| 2026-11-18 05:41 | The new check catches a backup cut short by a network drop on the control-plane node and stops the upgrade before the drain. A retry works |

## Root cause

The upgrade scripts run every remote command through a small helper, `q`,
that hides the Raspberry Pi OS login banner ("SSH may not work until a
valid user has been set up..."): it merges stderr into stdout and filters
the banner lines, and empty lines, out with `grep -v`. The backup was
written as `q "tar czf - ..." | ssh backup-host "cat > file"`. That sent
a binary gzip stream through a line filter. `grep` dropped every "line"
of the compressed data that happened to match or be empty, and the
result was a file with the right name and roughly the right size, but
broken after the first few hundred kilobytes.

Why it wasn't caught:

- The step only checked that the commands succeeded. `tar` and `cat`
  both exited 0.
- The size looked plausible (300-420 MB).
- Nothing ever read a backup back: no list, no test restore.

## What went well

- It was found by using a backup for something, before an emergency
  needed one.
- The datastore backups were built differently from the start, with a
  weekly automated restore test, so the most important backup was never
  in doubt.

## What changed

- **Binary streams never go through the banner filter.** The backup is
  plain `ssh`, stderr discarded, nothing in between.
- **The backup host checks the backup before it counts.** It writes to a
  `.part` file, lists it with `tar -tzf` (which reads the whole gzip
  stream), requires `etc/fstab` in the list, and only then renames it.
  The upgrade stops if any of that fails. This caught a real problem the
  next morning: a network drop cut a stream at 227 of 422 MB.
- **One retry, with keepalives,** so a short network drop doesn't stop an
  upgrade, but a broken backup still does.
- The upgrade write-ups have a "lessons" entry for it, and the
  replace-a-node runbook starts with "a fresh, checked backup".

## Lessons

- A backup you haven't restored from is a hope, not a backup. Exit code
  0 and a plausible size are not evidence.
- Helpers that make output nicer (filter banners, merge stderr) are for
  text. Anything binary has to bypass them.
- The cheapest check (does it list to the end?) would have caught this
  on the first day.
