# 2026-11-16: an in-place 32-to-64-bit switch removed sshd from a worker

| | |
|---|---|
| Date | 2026-11-16, about 2.5 hours |
| Node | kube-slave3 (worker) |
| Impact on visitors | none measured (see "Impact") |
| Node out of rotation | 2 h 26 min (planned: ~30 min) |
| Detected by | the upgrade script's own post-check, before the reboot |
| Status | resolved; fixes verified on the other three nodes |

## Summary

After the Debian trixie upgrade, all four Pis ran a 64-bit kernel with a
32-bit (armhf) userland. I switched the userland to 64-bit (arm64) in
place, one node at a time, with a script rehearsed in a chroot copy of the
node. On the first real node, systemd's 32-bit package refused to be
removed because it was the running init, which the rehearsal had never
seen. The script's recovery step then ran `apt-get -f install`, which
resolved the conflict by removing about 100 packages, openssh-server and
NetworkManager among them. The final check caught the missing binaries
and stopped before the reboot, so the node stayed up and drained, but
without ssh. I repaired it through a Kubernetes debug pod on the node,
then let the script finish. No failed request was measured.

## Impact

- The node was drained for 2 h 26 min instead of ~30 min. The cluster ran
  on three of four nodes; every public app has two replicas on different
  nodes, so nothing went down.
- Visitors: the status checker (every 5 min, all 8 sites) and the external
  probe on the VPS (every 2 min, 78 runs during the window) recorded 0
  failures. The once-per-second probes of the upgrade script only cover
  the final 3 minutes, because the first run's probes ended with it, so
  single failed requests before those last minutes can't be ruled out. An
  outage of a minute or more can.
- SLOs: no error budget used.

## Timeline ( from the start of the real run )

| Time | Event |
|---|---|
| before, 21 min | Final full rehearsal in a chroot copy of the node: clean, 21 min |
| +0 | Real run starts: preflight, config backup, boot partition copy |
| +5-7 min | Gentle drain (10 pods, one at a time), Kong gateway off on the node |
| +8-20 min | Phases A (purge leftovers), B (Raspbian armhf -> Debian armhf), C1 (dpkg, apt, tar, mawk -> arm64): fine |
| +20 min | C2 (everything else, one transaction), round 1: `systemd:armhf`'s prerm refuses to remove the running init. apt stops |
| ~+24 min | The recovery step runs `apt-get -f install`. It removes ~100 packages that are not armhf, including openssh-server and network-manager |
| +24-34 min | C2 round 2 and C3 (purge armhf, drop the architecture) finish |
| +34 min | Final check fails: sshd and NetworkManager missing. The script stops **before the reboot** and leaves the node drained |
| +34 min to +1 h 43 min | Investigation. The network interfaces stay up, so the node is still in the cluster (Ready, drained), but ssh is gone |
| +1 h 43 min to +2 h 17 min | Repair via `kubectl debug node/kube-slave3` (chroot into /host, a transient systemd unit): reinstall 118 missing packages (a reference node's list minus what phase A removes on purpose), 0 removals, configs intact (removed, not purged) |
| +2 h 31 min | Script resumed (`RESUME=1`) |
| +2 h 32-33 min | Reboot, 48 s; all services up |
| +2 h 33 min | Gateway back on, node uncordoned |

## Root cause

Two things together:

1. **The rehearsal wasn't a live system.** systemd's maintainer script
   checks `/run/systemd/system` to tell whether systemd is the running
   init, and refuses to be removed if it is. The chroot copy had no
   `/run/systemd/system`, so the rehearsal removed and replaced it without
   complaint, five rounds in a row.
2. **The recovery was too trusting.** After a stopped transaction, the
   script ran `apt-get -f install` to let apt repair dependencies. In a
   half-switched system apt's cheapest "repair" is to remove whatever
   blocks it, and in the chroot it had only ever removed 32-bit packages.
   On the live node it removed 64-bit ones too, sshd included. Nothing
   checked what it was about to remove.

## What went well

- The post-check before the reboot. Rebooting into a node without
  NetworkManager and sshd would have meant pulling the SD card.
- The node had been drained gently and its gateway was off, so a broken
  node never served traffic.
- `kubectl debug node/` is a way in that doesn't depend on the node's own
  ssh. It turned an SD card job into a remote repair.
- Packages were removed, not purged, so every config file was still there.

## What changed

- **systemd moves alone, first.** A new phase installs `systemd:arm64`
  with `dpkg --install`. dpkg then treats it as an upgrade of the same
  package, not a removal, and the prerm doesn't object. udev stays in the
  big transaction: in the rehearsal, moving it early wanted to remove the
  kernel and NetworkManager, because one library is a Raspbian rebuild
  with a different version.
- **Simulate, then refuse.** Every risky apt step is simulated first
  (`apt-get -s`), and the step stops if the plan removes anything that
  isn't armhf. The recovery's `-f install` has the same guard.
- **The rehearsal behaves like a live system.** `/run` in the chroot is
  a tmpfs with `/run/systemd/system` in it. The rehearsal is a script now,
  not a sequence of commands.
- **The rehearsal compares package sets.** Comparing the rehearsal's
  result with a reference node, package by package, found five packages
  the switch dropped silently (rpi-eeprom among them). A final phase puts
  them back and restores apt's auto/manual marks.

Result: the other three nodes switched on 2026-11-17 and 2026-11-18 in
one go each, with no recovery, 20-42 minutes per node, and 0 failed
requests over the whole run (the probes covered all of it).

## Lessons

- A rehearsal only proves what it reproduces. "Is this the running init?"
  is exactly the kind of state a chroot doesn't have.
- An automatic fix needs a limit on what it may do, checked before it
  does it, not afterwards.
- A check that runs before the point of no return (here: the reboot) is
  worth more than any amount of checking after it.
