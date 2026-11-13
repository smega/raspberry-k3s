# pi-temp-led

An RGB LED per Raspberry Pi node that shows its own CPU load: blue when
idle, red when busy (or CPU temperature, with `LED_SOURCE=temp`). Runs as a Kubernetes DaemonSet, so plugging a new
Pi into the cluster is the only setup step -- one pod per node, each
watching and lighting up only its own hardware.

Rebuilt from a ~2018-2019 experiment on the same 4-Pi cluster (the original
scripts drove the LED off CPU load too; this version adds a calmer
blue-to-red gradient and an optional temperature mode).

## Hardware

Common-cathode (or common-anode, see below) RGB LED, one per node, wired to:

| Color | BCM pin |
|---|---|
| Red | 12 |
| Green | 16 (unused by the color logic, wired for completeness) |
| Blue | 20 |

Override pins with `LED_RED_PIN` / `LED_GREEN_PIN` / `LED_BLUE_PIN` if wired
differently. If your LED is common-anode, set `LED_COMMON_ANODE=1` (inverts
every duty cycle) instead of rewiring.

## Wiring self-test

On every start (and restart), each pod steps its LED through red -> green ->
blue -> off, 3s each (`LED_SELFTEST_SECONDS`, 0 disables it), logging which
BCM pin it's driving at each step. Useful for confirming per-node that the
LED is actually wired to the pins the config claims - watch the physical
LED on each Pi against:

```sh
kubectl -n cpu-led logs -l app=pi-temp-led --prefix | grep selftest
```

## How the color is picked

Default (`LED_SOURCE=load`): reads whole-node CPU utilisation from
`/proc/stat` every second (`LED_POLL_SECONDS`), smooths it
(`LED_LOAD_SMOOTHING`, 0..1, default 0.35, higher reacts faster), and blends
blue -> red. These Pis idle at 1-10% CPU, so the default `LED_LOAD_CURVE=sqrt`
stretches the low end: 4% load is already 20% red, 25% is half-way, and only
100% is full red. `LED_LOAD_CURVE=linear` maps load straight to color.

`LED_SOURCE=temp`: reads `/sys/class/thermal/thermal_zone0/temp` and blends
blue -> red between `LED_TEMP_MIN_C` (default 35, full blue at/below) and
`LED_TEMP_MAX_C` (default 75, full red at/above).

No green component either way: it's a straight two-color gradient, not a
traffic-light scheme.

## Deploying

```sh
kubectl apply -f deployments/00-namespace.yaml
kubectl apply -f deployments/01-daemonset.yaml
```

Needs `/dev/gpiomem` and real hardware on every node, so the container runs
`privileged: true` -- it's the simplest reliable way to get GPIO access
from inside a pod, and this workload is cluster-internal only (no K8s API
access, no network exposure). `automountServiceAccountToken: false` since
it never talks to the Kubernetes API.

Logs per node (handy for checking a specific Pi without plugging in a
monitor):

```sh
kubectl -n cpu-led logs -l app=pi-temp-led --prefix
```

## GPIO library note

Uses [`rpi-lgpio`](https://pypi.org/project/rpi-lgpio/), a drop-in
replacement for `RPi.GPIO` built for the gpiochip-based driver Raspberry Pi
OS Bookworm ships -- classic `RPi.GPIO` pokes BCM registers directly via
`/dev/gpiomem` and can misbehave on Bookworm kernels. If your nodes run an
older OS (Buster/Bullseye) and this doesn't work, swap `rpi-lgpio` for
`RPi.GPIO` in `app/requirements.txt`; the code imports `RPi.GPIO` either
way, so nothing else changes.

Runs on Raspberry Pi OS Bookworm (64-bit) on all four nodes of the cluster
this was built for.
