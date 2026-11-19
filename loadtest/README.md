# Load test

How much traffic the cluster serves before it gets slow, measured with k6
from inside the cluster against Kong (no Cloudflare, no VPS in the way).

```
kubectl apply -f namespace.yaml
TARGET=web RATES=25,50,100,200,300,400,600,800,1000 OUT=~/data ./run.sh
TARGET=api RATES=10,25,50,100,200,300,400,600 OUT=~/data ./run.sh
kubectl delete ns loadtest
```

`ramp.js` sends requests at a fixed rate that steps up every minute (one
k6 scenario per step, so latency and errors come out per step) and stops
by itself once more than 5% fail or p95 passes 2 s. `run.sh` runs it as a
Job (`job.yaml`) and saves k6's summary, a per-step table and the
Prometheus series for the same window: node CPU, the target pods' CPU,
throttling and memory, Kong, the HPA, the status checker. An aborted run
leaves its Job "failed", which fires KubeJobFailed until the namespace
is deleted.

## Results (2026-11-19, k6 1.8.3 on kube-slave3)

**Status page** (static files, nginx, 2 pods scaled to 3 by the HPA,
150m CPU limit each):

| req/s | p50 ms | p95 ms | p99 ms | failed |
|---|---|---|---|---|
| 25-600 | 2-3 | 3-4 | 4-8 | 0 |
| 800 | 2 | 16 | 64 | 0 |
| 1000 | 2 | 88 | 198 | 0 |

**Status API** (`/api/status`, Go, 2 pods, 250m CPU limit each):

| req/s | p50 ms | p95 ms | p99 ms | failed |
|---|---|---|---|---|
| 10-100 | 6-7 | 9-11 | 13-17 | 0 |
| 200 | 145 | 717 | 1160 | 0 |
| 300 | 837 | 9205 | 10001 | 4.8% (stopped) |

What ran out was not the Pis: the pods sat at their CPU limits (status-web
0.15 cores each, throttled in up to 82% of scheduling periods; status-api
0.25, throttled ~100%), while the busiest node stayed under 80% (kube-master,
which also runs Prometheus) and the others under 45%. Kong's four gateways
used about 0.25 cores each at 1000 req/s. The status checker saw no failed
check during any run.

A cold jump straight to 1200 req/s, with the HPA still at 2 pods, fell
over in seconds (p95 4.2 s, 0.5% failed, stopped after ~30 s at about 650
req/s served): the HPA needs the ramp to scale before the load arrives. It
measures CPU against the request (30m), so under load it reads "500%".

### After the fixes (same day)

status-web's CPU limit 150m -> 300m and up to 4 replicas (542824e);
/api/status served from memory and gzipped when asked (1dd08a2).

| status page, req/s | p95 ms | p99 ms | failed |
|---|---|---|---|
| 600 | 7 | 12 | 0 |
| 1000 | 69 | 178 | 0 |
| 1200 | 140 | 285 | 0 |
| 1600 (1503 served) | 332 | 509 | 0 |
| 2000 (1529 served) | 612 | 930 | 0 |

| /api/status, req/s | p95 ms | p99 ms | failed |
|---|---|---|---|
| 100 | 8 | 12 | 0 |
| 200 | 22 | 68 | 0 (was p95 717) |
| 300 | 183 | 291 | 0 (was 4.8% failed) |
| 400 (~290 served) | 2838 | 3871 | 0, stopped |

The status page now tops out near 1500 req/s without an error. The API
went about 2x, but k6 didn't ask for gzip (11 GB for 57k requests), so
that's the cache alone; visitors through Cloudflare get the 17 KB gzipped
response. Its pods are at their 250m limit again, ~100% throttled, with
the nodes at 52-81%.

### Through the VPS (same day, after a 500m limit for status-api)

`VIA=vps ./run.sh`: k6 in docker on the VPS, the sites' own names resolved
to its nginx, gzip asked for as Cloudflare does. So TLS, nginx, Tailscale
and Kong are all in the path; only Cloudflare isn't.

| req/s | status page p50 / p95 ms | /api/status p50 / p95 ms | failed |
|---|---|---|---|
| 10-200 | 95-96 / 99-100 | 142 / 147-149 | 0 |
| 400 | 96 / 101 | 147 / 165 | 0 |
| 600 | 98 / 105 | | 0 |
| 800 | 100 / 109 | | 0 |

- ~95 ms is the floor: the VPS opens a new connection over Tailscale to a
  node for every request (no keepalive pool, on purpose since the
  node-crash game day), two round trips to the home line each time.
- /api/status no longer hit its CPU limit (0.3 of 0.5 cores), but all four
  nodes went to 85-93% CPU at 400 req/s. On a worker at 300 req/s:
  tailscaled ~50% of a core, containerd ~33%, k3s-agent ~25%, Alloy ~25%,
  Kong ~17%. Tailscale's WireGuard runs in user space, and status-api's
  per-request log line (with the trace id) costs containerd, Alloy and
  Loki work at that rate.

Caveats: one run per target, the load generator on a node of the same
cluster (its CPU counts on kube-slave3), compressed responses discarded,
and nothing in front of Kong; through the VPS and Cloudflare the numbers
would include those hops.
