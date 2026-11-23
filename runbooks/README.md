# Runbooks

One page per alert defined in this repo. Every rule links its page in the
`runbook_url` annotation, and the phone notification (ntfy) carries the
link, so an alert arrives with "what now" attached.

Each page: what fired, what it means for visitors, how to check, how to
fix, and what happened the last times.

A minute after the alert, a second, quiet notification follows with the
first things to check, collected automatically by
[alert-diagnostics](../alert-diagnostics/main.go): the target's Kubernetes
events, its last log lines ( Loki ), its last rollout and the git revision
Flux runs, the error budgets, and what else is firing. It is a separate
webhook, so the alert itself never waits for it or depends on it.

| Alert | Severity | Rule |
|---|---|---|
| [StatusSLOFastBurn](StatusSLOFastBurn.md) | critical | `deployments/11-status-slo-alerts.yaml` |
| [StatusSLOSlowBurn](StatusSLOSlowBurn.md) | warning | `deployments/11-status-slo-alerts.yaml` |
| [StatusCheckerStale](StatusCheckerStale.md) | critical | `deployments/11-status-slo-alerts.yaml` |
| [StatusMetricsMissing](StatusMetricsMissing.md) | critical | `deployments/11-status-slo-alerts.yaml` |
| [TrafficErrorsFastBurn](TrafficErrorsFastBurn.md) | critical | `deployments/17-traffic-slo.yaml` |
| [TrafficErrorsSlowBurn](TrafficErrorsSlowBurn.md) | warning | `deployments/17-traffic-slo.yaml` |
| [TrafficLatencySLOBurn](TrafficLatencySLOBurn.md) | warning | `deployments/17-traffic-slo.yaml` |
| [ImageVulnerabilityFixable](ImageVulnerabilityFixable.md) | warning ( info for third-party images ) | `trivy-operator/alerts.yaml` |
| [CertificateExpiresSoon](CertificateExpiresSoon.md) | warning | `deployments/12-cert-expiry.yaml` |
| [CertificateExpiresVerySoon](CertificateExpiresVerySoon.md) | critical | `deployments/12-cert-expiry.yaml` |
| [TLSProbeFailing](TLSProbeFailing.md) | warning | `deployments/12-cert-expiry.yaml` |
| [CanaryRolledBack](CanaryRolledBack.md) | warning | `flagger/alerts.yaml` |
| [CanaryStuck](CanaryStuck.md) | warning | `flagger/alerts.yaml` |
| [UnusualReleaseRate](UnusualReleaseRate.md) | warning | `monitoring/release-rate-rules.yaml` |
| [LogPanic](LogPanic.md) | warning | `observability/loki-rules.yaml` ( Loki ) |
| [LogTraceback](LogPanic.md) | warning | `observability/loki-rules.yaml` ( Loki ) |
| [LogConnectionErrors](LogConnectionErrors.md) | warning | `observability/loki-rules.yaml` ( Loki ) |
| [DeployFreeze](DeployFreeze.md) | warning | `deployments/15-deploy-freeze.yaml` |
| [NodeCordonedTooLong](NodeCordonedTooLong.md) | warning | `monitoring/maintenance-rules.yaml` |
| [NodeRootDiskFilling](NodeRootDiskFilling.md) | warning | `monitoring/node-disk-rules.yaml` |
| [ClusterDNSDown](ClusterDNSDown.md) | critical | `monitoring/dns-probe.yaml` |
| [ClusterDNSDegraded](ClusterDNSDown.md) | warning | `monitoring/dns-probe.yaml` |
| [ExternalDNSFailing](ClusterDNSDown.md) | warning | `monitoring/dns-probe.yaml` |
| [SDCardSlowing](SDCardSlowing.md) | warning | `monitoring/sd-card-health.yaml` |
| [SDCardKernelErrors](SDCardKernelErrors.md) | warning | `monitoring/sd-card-health.yaml` |
| [NodeRootFilesystemReadOnly](SDCardKernelErrors.md) | critical | `monitoring/sd-card-health.yaml` |
| [SDCardProbeMissing](SDCardKernelErrors.md) | warning | `monitoring/sd-card-health.yaml` |

`NodeMaintenance` (same file) has no page: it notifies no one, it only
inhibits the drain and reboot noise while a node is cordoned.

The kube-prometheus-stack alerts (node, kubelet, Prometheus itself...)
come with their own `runbook_url`, pointing at
[runbooks.prometheus-operator.dev](https://runbooks.prometheus-operator.dev/).

A new alert gets a page here in the same commit.
