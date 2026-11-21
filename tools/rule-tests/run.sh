#!/bin/sh
# Unit tests for the PrometheusRules ( promtool, in the Prometheus image ).
# Each <name>.test.yaml tests deployments' <name> rule groups, extracted
# from the PrometheusRule into <name>.rules.yaml first.
set -eu
cd "$(dirname "$0")"
python3 -c "
import yaml
d = yaml.safe_load(open('../../deployments/17-traffic-slo.yaml'))
yaml.safe_dump({'groups': d['spec']['groups']}, open('traffic-slo.rules.yaml', 'w'), sort_keys=False)
"
docker run --rm -v "$PWD:/t" -w /t --entrypoint promtool quay.io/prometheus/prometheus:v3.5.0 \
  test rules traffic-slo.test.yaml
rm -f traffic-slo.rules.yaml
