# Raspberry Pi k3s lab

A small, public-safe demo running on a home Raspberry Pi k3s cluster. Live at:

- [status.smega.eu](https://status.smega.eu) -- a public status page for the
  small services I run myself, checked every 5 minutes by a Kubernetes
  CronJob on this same cluster, plus live CPU temperature, CPU and memory
  usage of each Pi in the cluster.
- [whoami.smega.eu](https://whoami.smega.eu) -- a minimal ingress/routing demo
  ([traefik/whoami](https://github.com/traefik/whoami)).
- [cluster.smega.eu](https://cluster.smega.eu) -- a readonly dashboard showing
  selected live cluster state (pods, services, deployments, nodes, etc), also
  open source: [cluster-dashboard](https://github.com/smega/cluster-dashboard).
- `lab.smega.eu` / `api-lab.smega.eu` are reserved, not currently routed to
  anything.

## Layout

- `frontend/` -- Preact status page served at status.smega.eu.
- `backend/` -- Go/Gin API serving `frontend/`'s data (api-status.smega.eu). A
  thin read-only layer: it just serves whatever JSON the checker last wrote.
  `/api/nodes` adds live per-node stats (CPU temperature, CPU, memory, load,
  uptime, Ready/reachable state, and 24h temperature/CPU/memory history)
  from the cluster's own Prometheus (kube-prometheus-stack + node-exporter
  + kube-state-metrics, `PROMETHEUS_URL`). The node list comes from
  kube-state-metrics, so a Pi that is down still shows up, marked
  unreachable. It only runs
  fixed queries, caches the answer for 15s, and labels nodes as
  "Pi #N - role" instead of exposing hostnames or IPs.
  `/api/release` reports the last canary release of the status page
  (promoted or rolled back, when, how long), read from Flagger's
  `flagger_canary_status` series.
  `/badge/<service>.svg` serves a shields-style uptime badge per service
  (slug of its name, e.g. `personal-site`), for READMEs.
  On a separate, unrouted port (9100) it also exposes the checker's results
  as Prometheus metrics (`status_service_up`, latency, data age), which feed
  the SLO burn-rate alerts in `deployments/11-status-slo-alerts.yaml`.
- `.github/workflows/` -- CI: every push to main builds the images whose
  directory changed (arm64, `main-<unix time>-<sha>` tags), signs them with
  cosign and scans them with Trivy (see "CI/CD" below).
- `cron/` -- a Python CronJob (`check_status.py`) that checks each configured
  service every 5 minutes, keeps a rolling 24h history per service, and
  patches the result into a ConfigMap.
- `pi-temp-led/` -- a DaemonSet that drives an RGB LED on every Pi from its
  own CPU load (blue when idle, red when busy), plus its own manifests and
  README. The status page's per-node dot uses the same color scale.
- `clusters/home/` -- Flux: the cluster follows this repo (see "GitOps"
  below).
- `monitoring/` -- kube-prometheus-stack and the blackbox exporter as Flux
  HelmReleases, their values files, and Prometheus's storage (a local PV on
  a USB stick in the control-plane Pi).
- `kong/` -- Kong (the cluster's Gateway API implementation) as a Flux
  HelmRelease and its values: 2 gateway and 2 controller replicas on
  different Pis.
- `content/` -- text the status page loads at runtime (the incident log),
  served from a ConfigMap: a new entry is a commit, no image build.
- `runbooks/` -- one page per alert defined here (what fired, impact,
  how to check, how to fix, history); each rule links its page in
  `runbook_url`.
- `postmortems/` -- blameless write-ups of what went wrong and what changed
  afterwards; the short versions are in the status page's incident log.
- `flagger/` -- Flagger (canary releases through the Gateway API) and its
  load tester, which runs the canary checks; see "Canary releases" below.
- `kured/` -- automatic, one-at-a-time node reboots after OS updates
  (Mon/Tue/Thu 03:00-06:00, never while an alert is firing).
- `system-upgrade/` -- system-upgrade-controller and the Plans that keep
  k3s on the latest 1.37.x (Wednesdays 02:30-06:00).
- `deployments/` -- the rest of the Kubernetes manifests (namespaces, RBAC,
  the apps above, the Gateway/HTTPRoutes, and the readonly dashboard),
  applied roughly in filename order.

## CI/CD

```
git push main
  -> GitHub Actions (images.yml -> build-image.yml, arm64 runner)
       build, push registry.smdoes.eu/raspberry/<image>:main-<time>-<sha>
       cosign sign (keyless, GitHub OIDC), Trivy -> Security tab
  -> Flux image automation (image-automation/)
       ImagePolicy picks the newest main-* tag
       ImageUpdateAutomation commits it into the manifest ("Deploy new images")
  -> Flux applies that commit like any other change
  -> policy-controller checks the signature when the pod is created
       (image-policies/: signed by build-image.yml on main, or a warning)
  -> status-web only: Flagger releases it as a canary (below)
```

No `:latest` anywhere: a tag is never moved, so every pod runs exactly the
build git names, and a rollback is a `git revert` of the "Deploy new
images" commit. The manifests carry `# {"$imagepolicy": ...}` markers that
tell Flux which line to update. smega/cluster-dashboard uses the same
build workflow for its two images. Actions are pinned to commit SHAs;
Renovate (`renovate.json`) proposes updates to them, to base images and to
the Helm charts, never to the images Flux deploys.

### Canary releases

A new status-web image doesn't replace the running one. Flagger
(`flagger/`, the Canary in `deployments/14-status-web-canary.yaml`) keeps
the live version as `status-web-primary` and starts the new build next to
it, then:

```
pre-rollout   smoke test against the canary alone, before any traffic
every 30s     smoke test again + no container restarts (Prometheus),
              then +10% of the traffic (HTTPRoute backend weights, Kong)
at 50%        promote: the primary gets the new version, canary to zero
3 failures    roll back: all traffic to the primary, CanaryRolledBack alert
```

About 4 minutes either way. The smoke test fetches the page, the JS bundle
it references and the incident log JSON from the canary Service. The gate
is synthetic on purpose: Kong builds one upstream from both weighted
backends, so its metrics can't say which version answered, and 10% of
this page's traffic is too few requests to judge. Flagger owns the
`status-web` Services and HTTPRoute; the replica count is an HPA (min 2)
so Flux doesn't fight Flagger's scaling; the incident log ConfigMap is
left out of Flagger's config tracking, so text still updates in place.

**Rollback test (2026-11-19):** a deliberately broken build (the page's
root element renamed: the build passes, the page renders empty) failed
the pre-rollout smoke test three times and was rolled back after about 2
minutes without getting a single request; a probe through Cloudflare
every second saw 609 good pages and no errors through the whole test.

## Readonly dashboard: what it can and can't see

`public-dashboard-sa` (the dashboard backend's service account) is granted a
`ClusterRole` with `get/list/watch` on a fixed set of resource types (pods,
services, deployments, nodes, jobs, RBAC objects, etc) -- deliberately
**excluding Secrets**. A public readonly dashboard has no legitimate reason to
read Secret contents, and since this is a ClusterRole (not namespace-scoped),
including it would let the dashboard's own token read every Secret on the
cluster. See `deployments/09-dashboard-readonly-rbac.yaml`.

## GitOps (Flux)

The cluster runs [Flux](https://fluxcd.io) and follows the `main` branch of
this repo: a commit is a deploy, nothing gets `kubectl apply`'d by hand.

```
clusters/home/
  flux-system/   Flux itself (pinned install.yaml) + "follow this repo"
  monitoring.yaml  -> ./monitoring          (HelmReleases, PV)
  kong.yaml        -> ./kong                (Kong HelmRelease)
  kured.yaml       -> ./kured               (automatic node reboots)
  system-upgrade.yaml -> ./system-upgrade   (k3s patch upgrades)
  image-automation.yaml -> ./image-automation (new image tags -> git)
  policy-controller.yaml -> ./policy-controller, ./image-policies
                                            (image signature checks)
  apps.yaml        -> ./deployments         (after monitoring)
                    -> ./pi-temp-led/deployments
  private.yaml     -> a private repo        (probes with private hostnames)
  notifications.yaml  failed reconciles -> ntfy (same topic as the alerts)
```

Secrets live in the private repo, encrypted with SOPS + age; Flux decrypts
them. Only two are made by hand, because Flux needs them before it can read
anything: the age key and the private repo's deploy key.

**Rebuild test (2026-11-18):** an empty k3s (in Docker, same version, 4
nodes), those two Secrets, then Flux from this repo: every Kustomization and
HelmRelease Ready and every Deployment available after **9.6 minutes**. Only
the hardware-bound parts stay down (the GPIO LED driver, Prometheus's
USB-stick volume). The first run never got there: the `kong` and `monitoring`
namespaces and two charts' CRDs existed only on the live cluster (now in
git: Namespace manifests, `install.crds: Create`), and failed applies
waited their 30-minute interval (now `retryInterval: 1m`).

The status ConfigMap is seed-only (`kustomize.toolkit.fluxcd.io/ssa:
IfNotPresent`): the checker owns its data, Flux only creates it if missing.

Everything public-facing runs at least 2 replicas spread over two Pis, with
a PodDisruptionBudget (`deployments/13-disruption-budgets.yaml`), so a node
drain or reboot never takes a site down.

To run your own copy without Flux: install the charts in `monitoring/` and
`kong/` with helm (same values files), then `kubectl apply -k deployments/`.

Notes for your own copy:
- `11-status-slo-alerts.yaml` and `12-cert-expiry.yaml` need
  kube-prometheus-stack (the ServiceMonitor/PrometheusRule/Probe CRDs, and a
  Prometheus selecting `release: grafana` - change the label to your release
  name). They alert on the page's own SLO (99.5% over 30 days) with
  multi-window, multi-burn-rate rules, on the checker going stale, and on TLS
  certificates close to expiry.
- `07-gateway.yaml` uses `gatewayClassName: kong` (the Helm chart in
  `kong/kong-values.yaml`) and lives in the `kong` namespace the Kong release
  creates. Change the class if you run another Gateway API implementation.
- The dashboard needs a `dashboard-secrets` Secret (`mongo_uri`,
  `jwt_secret`), created separately.
- The Gateway only accepts HTTPRoutes from namespaces labelled
  `expose-via-public-gateway: "true"` (see `00-namespaces.yaml`), so a new
  public app means labelling its namespace, not touching the Gateway.

## Configuring which services the status page checks

`cron/check_status.py` ships with a default `name -> url` map. Override it
without rebuilding the image by setting `STATUS_TARGETS_JSON` on the
CronJob's container to a JSON object of the same shape.
