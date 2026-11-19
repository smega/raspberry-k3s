#!/usr/bin/env bash
# One load test run: the k6 Job (job.yaml), then the run's numbers into
# OUT/<time>-<target>/: k6's summary, a per-step table, and Prometheus
# series for the same window (nodes, the target's pods, Kong, HPA).
#
#   TARGET=web RATES=25,50,100 NODE=kube-slave3 OUT=~/loadtest-data ./run.sh
set -euo pipefail
cd "$(dirname "$0")"
: "${TARGET:=web}" "${RATES:=25,50,100,200,300,400,600}" "${STEP:=1m}" "${NODE:=kube-slave3}" "${BUNDLE:=/}"
: "${OUT:=./data}"
export TARGET RATES STEP NODE BUNDLE
case $TARGET in
  web|bundle) PODS='status-web.*' ;;
  api|nodes) PODS='status-api.*' ;;
esac
dir="$OUT/$(date -u +%Y%m%dT%H%M%SZ)-$TARGET"
mkdir -p "$dir"

kubectl apply -f namespace.yaml >/dev/null
kubectl -n loadtest create configmap k6-script --from-file=ramp.js --dry-run=client -o yaml | kubectl apply -f - >/dev/null
if [ "${POSTONLY:-0}" = 1 ]; then
  # Numbers for a Job that already ran (its pod still there).
  ts() { date -j -u -f '%Y-%m-%dT%H:%M:%SZ' "$1" +%s 2>/dev/null || date -u -d "$1" +%s; }
  start=$(ts "$(kubectl -n loadtest get "job/k6-$TARGET" -o jsonpath='{.status.startTime}')")
else
  kubectl -n loadtest delete job "k6-$TARGET" --ignore-not-found >/dev/null
  start=$(date +%s)
  envsubst < job.yaml | kubectl apply -f - >/dev/null
  echo "k6-$TARGET running: $RATES req/s, $STEP each"
fi
# kubectl wait with two --for conditions waits for both; poll instead.
until [ -n "$(kubectl -n loadtest get "job/k6-$TARGET" -o jsonpath='{.status.succeeded}{.status.failed}')" ]; do sleep 10; done
end=$(date +%s)
[ "${POSTONLY:-0}" = 1 ] && end=$(ts "$(kubectl -n loadtest get "job/k6-$TARGET" -o jsonpath='{.status.completionTime}')")
kubectl -n loadtest logs "job/k6-$TARGET" > "$dir/k6.log"
awk '/=====K6-SUMMARY=====/{f=1;next} /=====END=====/{f=0} f' "$dir/k6.log" > "$dir/k6-summary.json"
printf '{"target":"%s","rates":"%s","step":"%s","k6_node":"%s","start":%s,"end":%s}\n' \
  "$TARGET" "$RATES" "$STEP" "$NODE" "$start" "$end" > "$dir/run.json"

# Prometheus, through a port-forward, for [start-60s, end+60s] at 15s.
kubectl -n monitoring port-forward svc/grafana-kube-prometheus-st-prometheus 19090:9090 >/dev/null 2>&1 &
pf=$!; trap 'kill $pf 2>/dev/null' EXIT; sleep 3
q() {
  curl -sG http://127.0.0.1:19090/api/v1/query_range --data-urlencode "query=$2" \
    --data-urlencode "start=$((start - 60))" --data-urlencode "end=$((end + 60))" --data-urlencode step=15 \
    > "$dir/prom-$1.json"
}
q node_cpu '100 * (1 - avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[1m])))'
q pod_cpu "sum by (pod) (rate(container_cpu_usage_seconds_total{namespace=\"public-demo\",container!=\"\",pod=~\"$PODS\"}[1m]))"
q pod_throttled "sum by (pod) (rate(container_cpu_cfs_throttled_periods_total{namespace=\"public-demo\",pod=~\"$PODS\"}[1m])) / sum by (pod) (rate(container_cpu_cfs_periods_total{namespace=\"public-demo\",pod=~\"$PODS\"}[1m]))"
q pod_mem "sum by (pod) (container_memory_working_set_bytes{namespace=\"public-demo\",container!=\"\",pod=~\"$PODS\"})"
q kong_cpu 'sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="kong",container="proxy"}[1m]))'
q hpa 'kube_horizontalpodautoscaler_status_current_replicas{namespace="public-demo"}'
q checker 'min by (slug) (status_service_up)'

python3 - "$dir" <<'PY'
import json, sys
d = sys.argv[1]
m = json.load(open(f"{d}/k6-summary.json"))["metrics"]
run = json.load(open(f"{d}/run.json"))
rates = run["rates"].split(",")
st = run["step"]
secs = float(st[:-1]) * (60 if st.endswith("m") else 1)
rows = ["| req/s asked | served/s | p50 ms | p95 ms | p99 ms | failed | dropped |", "|---|---|---|---|---|---|---|"]
for r in rates:
    k = lambda n: m.get(f"{n}{{rate:{r}}}", {}).get("values", {})
    dur, fail, reqs, drop = k("http_req_duration"), k("http_req_failed"), k("http_reqs"), k("dropped_iterations")
    if not reqs.get("count"):
        rows.append(f"| {r} | (not reached) | | | | | |"); continue
    rows.append(f"| {r} | {reqs['count'] / secs:.0f} | {dur.get('med', 0):.0f} | {dur.get('p(95)', 0):.0f} | {dur.get('p(99)', 0):.0f} | {fail.get('rate', 0) * 100:.1f}% | {drop.get('count', 0):.0f} |")
open(f"{d}/steps.md", "w").write("\n".join(rows) + "\n")
print("\n".join(rows))
PY
echo "saved in $dir"
