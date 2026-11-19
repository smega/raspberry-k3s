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

Caveats: one run per target, the load generator on a node of the same
cluster (its CPU counts on kube-slave3), compressed responses discarded,
and nothing in front of Kong; through the VPS and Cloudflare the numbers
would include those hops.
