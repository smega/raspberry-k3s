#!/usr/bin/env python3
"""Measured usage against requests and limits, per container.

  python3 tools/resource-report.py [cpu window] [memory window]

Asks Prometheus through the API server ( kubectl must work ):
  cpu p95 / max   rate over 5m, in millicores, over the CPU window ( 36h )
  mem max         peak working set in MiB, over the memory window ( 7d )
  thr%            p95 of the share of CPU periods that were throttled

Pick a CPU window without load tests or game days in it, or their peaks
become the p95. How the numbers were turned into requests ( 2026-11-21 ):
CPU request ~ p95, memory request ~ 7-day peak + 15%, memory limit ~2x
the peak; CPU limits only where normal running is throttled.
"""
import collections
import json
import subprocess
import sys
import urllib.parse

CPU_WINDOW = sys.argv[1] if len(sys.argv) > 1 else "36h"
MEM_WINDOW = sys.argv[2] if len(sys.argv) > 2 else "7d"
PROM = "/api/v1/namespaces/monitoring/services/grafana-kube-prometheus-st-prometheus:9090/proxy/api/v1/query?query="
SEL = 'container!="",container!="POD"'


def query(expr):
    out = subprocess.run(["kubectl", "get", "--raw", PROM + urllib.parse.quote(expr)],
                         capture_output=True, text=True, check=True).stdout
    return json.loads(out)["data"]["result"]


def workload(pod):
    """deployment-5d9f8-abcde -> deployment, statefulset-0 -> statefulset."""
    parts = pod.split("-")
    if len(parts) > 2 and len(parts[-1]) == 5:
        parts = parts[:-1]
    if len(parts) > 1 and len(parts[-1]) in (8, 9, 10) and any(c.isdigit() for c in parts[-1]):
        parts = parts[:-1]
    if len(parts) > 1 and parts[-1].isdigit():
        parts = parts[:-1]
    return "-".join(parts)


rows = collections.defaultdict(dict)


def collect(name, expr, scale):
    for r in query(expr):
        m = r["metric"]
        key = (m["namespace"], workload(m.get("pod", "")), m["container"])
        rows[key][name] = max(float(r["value"][1]) * scale, rows[key].get(name, 0))


collect("cpu_p95", f"quantile_over_time(0.95, rate(container_cpu_usage_seconds_total{{{SEL}}}[5m])[{CPU_WINDOW}:5m])", 1000)
collect("cpu_max", f"max_over_time(rate(container_cpu_usage_seconds_total{{{SEL}}}[5m])[{CPU_WINDOW}:5m])", 1000)
collect("mem_max", f"max_over_time(container_memory_working_set_bytes{{{SEL}}}[{MEM_WINDOW}])", 1 / 2**20)
collect("req_cpu", f'kube_pod_container_resource_requests{{resource="cpu",{SEL}}}', 1000)
collect("req_mem", f'kube_pod_container_resource_requests{{resource="memory",{SEL}}}', 1 / 2**20)
collect("lim_cpu", f'kube_pod_container_resource_limits{{resource="cpu",{SEL}}}', 1000)
collect("lim_mem", f'kube_pod_container_resource_limits{{resource="memory",{SEL}}}', 1 / 2**20)
collect("thr", "quantile_over_time(0.95, (rate(container_cpu_cfs_throttled_periods_total{%s}[5m])"
        " / clamp_min(rate(container_cpu_cfs_periods_total{%s}[5m]), 0.001))[%s:5m])" % (SEL, SEL, CPU_WINDOW), 100)


def fmt(v):
    return "-" if v is None else f"{v:.0f}"


print(f'{"namespace/workload/container":60} {"cpu p95":>7} {"max":>5} {"req":>5} {"lim":>5} | {"mem max":>7} {"req":>5} {"lim":>5} | thr%')
for key in sorted(rows):
    d = rows[key]
    if "cpu_p95" not in d:
        continue
    flag = "  <- memory over 80% of limit" if d.get("lim_mem") and d.get("mem_max", 0) > 0.8 * d["lim_mem"] else ""
    print(f'{"/".join(key)[:60]:60} {fmt(d.get("cpu_p95")):>7} {fmt(d.get("cpu_max")):>5} {fmt(d.get("req_cpu")):>5} '
          f'{fmt(d.get("lim_cpu")):>5} | {fmt(d.get("mem_max")):>7} {fmt(d.get("req_mem")):>5} {fmt(d.get("lim_mem")):>5} '
          f'| {fmt(d.get("thr"))}{flag}')
