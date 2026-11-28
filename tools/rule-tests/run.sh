#!/bin/sh
# Unit tests for the PrometheusRules ( promtool, in the Prometheus image ).
# Each <name>.test.yaml tests the rule groups of one PrometheusRule,
# extracted into <name>.rules.yaml first.
set -eu
cd "$(dirname "$0")"
extract() {
  python3 -c "
import sys, yaml
d = yaml.safe_load(open(sys.argv[1]))
yaml.safe_dump({'groups': d['spec']['groups']}, open(sys.argv[2], 'w'), sort_keys=False)
" "$1" "$2"
}
# Every PrometheusRule in the given files, groups merged: an SLO's
# alerts read the recording rules Sloth generated ( 21-slos.generated.yaml ).
# The test files set the evaluation order ( group_eval_order ).
extract_all() {
  out=$1; shift
  python3 -c "
import sys, yaml
groups = []
for f in sys.argv[2:]:
    for d in yaml.safe_load_all(open(f)):
        if d and d.get('kind') == 'PrometheusRule':
            groups += d['spec']['groups']
yaml.safe_dump({'groups': groups}, open(sys.argv[1], 'w'), sort_keys=False)
" "$out" "$@"
}
extract_all traffic-slo.rules.yaml ../../deployments/21-slos.generated.yaml ../../deployments/17-traffic-slo.yaml
extract ../../monitoring/release-rate-rules.yaml release-rate.rules.yaml
extract ../../monitoring/node-runtime-rules.yaml node-runtime.rules.yaml status-slo.rules.yaml
extract_all status-slo.rules.yaml ../../deployments/21-slos.generated.yaml ../../deployments/11-status-slo-alerts.yaml
docker run --rm -v "$PWD:/t" -w /t --entrypoint promtool quay.io/prometheus/prometheus:v3.5.0 \
  test rules traffic-slo.test.yaml release-rate.test.yaml node-runtime.test.yaml status-slo.test.yaml
rm -f traffic-slo.rules.yaml release-rate.rules.yaml node-runtime.rules.yaml status-slo.rules.yaml
