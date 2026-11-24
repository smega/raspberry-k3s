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
extract ../../deployments/17-traffic-slo.yaml traffic-slo.rules.yaml
extract ../../monitoring/release-rate-rules.yaml release-rate.rules.yaml
extract ../../monitoring/node-runtime-rules.yaml node-runtime.rules.yaml status-slo.rules.yaml
python3 -c "
import yaml
d = [x for x in yaml.safe_load_all(open('../../deployments/11-status-slo-alerts.yaml')) if x and x.get('kind') == 'PrometheusRule'][0]
yaml.safe_dump({'groups': d['spec']['groups']}, open('status-slo.rules.yaml', 'w'), sort_keys=False)
"
docker run --rm -v "$PWD:/t" -w /t --entrypoint promtool quay.io/prometheus/prometheus:v3.5.0 \
  test rules traffic-slo.test.yaml release-rate.test.yaml node-runtime.test.yaml status-slo.test.yaml
rm -f traffic-slo.rules.yaml release-rate.rules.yaml node-runtime.rules.yaml status-slo.rules.yaml
