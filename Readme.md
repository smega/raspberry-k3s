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
  `/api/dora` reports the four DORA metrics for the status page over 30
  days (see "Delivery metrics" below), cached for 10 minutes.
  `/badge/<service>.svg` serves a shields-style uptime badge per service
  (slug of its name, e.g. `personal-site`), for READMEs.
  On a separate, unrouted port (9100) it also exposes the checker's results
  as Prometheus metrics (`status_service_up`, latency, data age, and
  `status_service_error` with the reason while a check fails), which feed
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
- `observability/` -- Loki, Tempo and Alloy: pod logs and traces from
  status-api and the checker, shown in Grafana (see "Logs and traces").
- `image-policies/` -- what the policy-controller requires of every own
  image at admission: the CI signature, an SBOM and SLSA provenance.
- `loadtest/` -- a k6 ramp test run as a Job, and its results.
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
       build, Trivy gate (fixable CRITICAL stops it), push
         registry.smdoes.eu/raspberry/<image>:main-<time>-<sha>
       cosign sign (keyless, GitHub OIDC), Trivy -> Security tab
       cosign attest: SBOM (SPDX, syft) + SLSA v1 provenance
  -> Flux image automation (image-automation/)
       ImagePolicy picks the newest main-* tag
       ImageUpdateAutomation commits it into the manifest ("Deploy new images")
  -> Flux applies that commit like any other change
  -> policy-controller checks, when the pod is created (image-policies/):
       signed by build-image.yml on main, an SBOM, and provenance whose
       builder is that workflow; otherwise the pod is refused
  -> status-web only: Flagger releases it as a canary (below)
```

No `:latest` anywhere: a tag is never moved, so every pod runs exactly the
build git names, and a rollback is a `git revert` of the "Deploy new
images" commit. The manifests carry `# {"$imagepolicy": ...}` markers that
tell Flux which line to update. smega/cluster-dashboard uses the same
build workflow for its two images. Actions are pinned to commit SHAs;
Renovate (`renovate.json`) proposes updates to them, to base images and to
the Helm charts, never to the images Flux deploys.

The provenance is written by the build job itself (SLSA Build L2, not L3),
and both attestations use cosign's legacy format (an `.att` tag): the
policy-controller can't find the newer bundle format, which is also why
GitHub's own `attest-build-provenance` isn't used. A cached rebuild of an
unchanged image gives the same digest, so the attestations cover every
tag that points at it.

### Dependency updates that merge themselves

Renovate opens its pull requests on Monday mornings, after a release has
been out for 14 days. Every pull request that changes an image's source is
built and scanned exactly as main would be ( `.github/workflows/pr-checks.yml`:
the tests in the Dockerfile, the Trivy gate; nothing pushed or signed ).
For the status page's own dependencies ( `frontend/`, `backend/` ) and
GitHub Actions, minor, patch and digest updates merge themselves once that
passes, on weekdays between 9 and 4, when I'm around. The merge is not the
last gate: the new image still goes out as a canary that rolls back by
itself. Helm charts, k3s, Flux and the checker stay manual
( `renovate.json` ).

### What a pull request would change

Every pull request that touches what Flux applies gets one comment, kept
up to date on each push ( `.github/workflows/flux-diff.yml` ): every
Flux Kustomization and HelmRelease rendered on main and on the PR
( flux-local, charts included ), the difference, and how many objects it
removes. The PR's objects are validated against their schemas first
( kubeconform, CRDs included ), and a broken one fails the check.

It compares main with the PR, not with the running cluster: `flux diff`
needs the API server, and CI has no way in. The private repo's
Kustomizations are skipped.

### Canary releases

A new status-web or status-api image doesn't replace the running one
( status-api since 2026-11-21, `deployments/18-status-api-canary.yaml`,
with a smoke test of the endpoints the page needs ). Flagger
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

### Error budget policy

When a service served from the cluster ( Cluster dashboard, Whoami demo )
has spent its 30-day error budget, new versions stop going out by
themselves ( `deployments/15-deploy-freeze.yaml` ):

```
decide    DeployFreeze alert: status_service_error_budget_remaining_ratio <= 0
          ( status-api exports the checker's budget, the number on the page )
enforce   deploy-freeze CronJob, every 5 min: suspends Flux's image
          automation while the alert fires, resumes it when it stops
```

The status page shows it under the last release on the Cluster tab: the
budget left while deploys run, and when and why they froze while frozen
( status-api's `/api/freeze`, read from the same alert ).

It only resumes a freeze it set itself ( annotation `smega.eu/frozen-by` ),
never a suspend made by hand, and if Prometheus can't be asked it changes
nothing. Only automatic tag updates stop: a hotfix goes out as a hand-made
commit of the image tag, applied by Flux like any other change. The
services on the VPS don't count; their failures say nothing about this
cluster's releases. Runbook: `runbooks/DeployFreeze.md`.

### SLOs from real traffic

The checker asks each site once every 5 minutes. Kong now also counts
every request visitors make, per route, with its status code and latency
( the prometheus plugin, `kong/metrics.yaml` ), and
`deployments/17-traffic-slo.yaml` turns that into a second set of SLOs:

```
availability  99.5% of requests not 5xx, every public route
latency       99% under 250 ms for the status page, under 2 s for its API
```

Same multi-window burn rates as the checker SLOs, with one addition for
low traffic: an alert also needs a minimum number of bad requests, so a
single 502 in a quiet hour doesn't page. The rules have promtool tests
( `tools/rule-tests/run.sh` ). The Cluster tab shows the last 24 hours
( `/api/traffic` ).

### Alerts that arrive with their diagnostics

Every alert that goes to my phone is followed by a quiet second message
with what I would look at first, so the first minutes of an incident
start from facts, not from opening laptops:

```
events       the pod's, workload's or node's Kubernetes events
logs         its last lines from Loki ( or errors in its namespace )
last change  its last rollout, the git revision Flux runs, Flux not ready
SLO          the error budgets, and what else is firing
```

`alert-diagnostics/` is a small Go service ( standard library only,
read-only RBAC ) that Alertmanager posts to next to ntfy
( `monitoring/alert-diagnostics.yaml` ). Separate webhooks: if it is down,
the alert still arrives, only without the follow-up.

### Pull request previews

A pull request labelled `preview` that changes `frontend/` gets its own
copy of the status page at `https://pr-<n>-status.smega.eu`, live a few
minutes after each push and deleted when the PR closes:

```
build    .github/workflows/preview.yml: the same build as main ( Trivy gate,
         signed, SBOM and provenance ), into registry.smdoes.eu/raspberry-preview/
         as pr-<n>-<head sha>, then a comment on the PR with the link
deploy   previews/: the Flux Operator's ResourceSet lists the labelled PRs
         every 5 min and keeps a namespace preview-pr-<n> per PR ( quota of
         1 pod, at most 2 previews ), applied by a ServiceAccount limited to
         those kinds, not the operator's cluster-admin
check    image-policies/raspberry-preview.yaml: signed by build-image.yml for
         a pull request of this repo; a ValidatingAdmissionPolicy refuses
         preview images in any namespace not labelled smega.eu/preview=true
edge     a *.smega.eu record to the VPS, which sends pr-*-status hosts to
         Kong and drops every other unknown host
```

The preview is the page only, reading the live status API. PRs from forks
are skipped: GitHub gives them no secrets. The registry deletes preview
images after 14 days.

### Delivery metrics (DORA)

The Cluster tab shows the four DORA metrics for status-web, computed by
status-api (`backend/dora.go`) from data that already exists, nothing
entered by hand:

```
deploy             a build that reached the live version: the first
                   status-web-primary pod running its tag (or, for builds
                   from before the canary, the first pod), from
                   kube-state-metrics (kube_pod_created + the image tag)
lead time          median from the commit (GitHub API, by the sha in the
                   tag main-<build time>-<sha>) to that pod
change failure     a canary Flagger rolled back (flagger_canary_status 2),
                   over all finished changes
time to restore    median from a failed release's start to its rollback
```

The window is 30 days, as long as Prometheus keeps history. Only
status-web goes through the canary, so only its releases count; platform
changes (Kong, k3s upgrades) that went wrong are in the incident log
instead. Commit dates come from one listing of the window's commits (a page
of 100), fetched again only when a deploy's commit isn't in it yet: the
home IP shares GitHub's 60 unauthenticated requests an hour with
everything else, and one request per commit used them up on the first
day.

## Logs and traces

`observability/` (Flux, `clusters/home/observability.yaml`):

```
pods' stdout --(Kubernetes API)--> Alloy --> Loki (3 days)
status-api, checker --(OTLP)-----> Alloy --> Tempo (24 h)
                                  Grafana: Prometheus, Loki, Tempo
```

- **Alloy** is one pod, not a DaemonSet: it reads every pod's log through
  the API (`loki.source.kubernetes`), so it needs no hostPath and no root,
  and four copies would cost ~300 MB on the Pis. A line with
  `trace_id=<32 hex>` gets that id as structured metadata (not a label:
  one value per request).
- **status-api** starts a span per request (not `/healthz`) and child
  spans for its calls to Prometheus and GitHub; it logs only failed
  (4xx/5xx) and slow (250 ms+) requests, each line with the trace id. **The checker** makes one trace per run, a span
  per site with the HTTP request under it; a failed check's line carries
  the trace id. Both export only when `OTEL_EXPORTER_OTLP_ENDPOINT` is set.
- **Grafana** (`monitoring/kube-prometheus-stack-values.yaml`): a log line
  links to its trace, a trace to the log lines with its id.
- Loki and Tempo run as single processes on kube-slave1/3 (not the
  master, which carries Prometheus, and not kube-slave2's slow SD card),
  all three under Pod Security `restricted`.

## Pod Security

`public-demo` and `public-dashboard` enforce Pod Security `restricted`
(`deployments/00-namespaces.yaml`): no pod there runs as root, adds a
capability, escalates privileges or runs without the runtime's seccomp
profile. What that took:

- the Go and Python images run as a fixed numeric user set in the
  manifest (the kubelet can't check `runAsNonRoot` against a user name);
- the nginx images run the official image as its nginx user (uid 101),
  pid and temp files in `/tmp` (an emptyDir; the root filesystem is
  read-only). Not `nginx-unprivileged`: it was a release behind with a
  CRITICAL CVE, and the CI's Trivy gate stopped the build;
- port 80 stays: the pods lower the unprivileged port range with the safe
  sysctl `net.ipv4.ip_unprivileged_port_start=0`, so no Service or route
  changed;
- pods that never call the Kubernetes API don't get a ServiceAccount
  token.

A CIS benchmark (kube-bench, k3s profile) run in a pod reports most
control-plane checks as failed on k3s: the profile reads the flags from
`journalctl`, which the image doesn't have. Checked on the host instead,
they pass (anonymous auth off, Node+RBAC, NodeRestriction, profiling off,
kubelet read-only port closed). Falco isn't running: the Raspberry Pi
kernel has no BTF, so its eBPF driver can't load without being compiled
for every kernel update.

## Requests from measured usage

Requests and limits were set by guess, and some by nobody: Prometheus and
Grafana had none, so the scheduler counted kube-master as nearly empty
while it was the busiest node. On 2026-11-21 every container was measured
against its settings ( `tools/resource-report.py`: CPU p95 over 36 hours
without load tests, memory peak over 7 days, throttling ):

```
found   tempo OOM-killed twice at its limit; alloy at 93% of its limit;
        the policy webhook at 85%; the status checker throttled through
        every run ( 67 s, now 21 s ); Flux reserving ~600m CPU for
        controllers using 2-35m; Alertmanager reserving 200Mi each for 33Mi
rule    CPU request ~ p95, memory request ~ peak + 15%, memory limit ~2x
        peak; CPU limits kept ( the load test set them ) unless normal
        running is throttled; no memory limit on Prometheus
```

kube-master's requested memory went from 7% to 42% of the node ( it uses
75% ). The public pages kept their CPU requests: their HPAs scale on a
percentage of them. CoreDNS and metrics-server are k3s's own manifests.
The Kong gateways rolled through the change with 0 failed requests
( 265, one a second, through Cloudflare ).

## Network policies: default deny

Until 2026-11-21 any pod could reach any pod. A test pod labelled like
the whoami demo, standing in for a compromised page, reached Prometheus,
Loki, Grafana, the Kubernetes API, a kubelet and the LAN router. Now the
public namespaces ( `public-demo`, `public-dashboard`, and every PR
preview ) deny everything by default and allow only what each app needs
( `deployments/16-network-policies.yaml`, enforced by k3s's built-in
kube-router ):

```
in    Kong gateways -> the public pages; Flagger's loadtester -> the canary;
      Prometheus -> status-api metrics; dashboard-web -> dashboard-backend
out   DNS for all; per app only Prometheus, Alloy, the API server, and
      HTTPS to the internet ( private ranges excluded )
```

The same test pod reaches nothing now, not even the internet; status-api
reaches exactly Prometheus, Alloy and HTTPS. `monitoring`, `observability` and `kong` deny
ingress by default too ( their `network-policies.yaml` ): only their own
pods, Prometheus, the API server and the known clients get in, so a pod
anywhere else can't read Prometheus, Loki or Alertmanager, or reach Kong's
admin API. Their egress stays open: Prometheus scrapes every namespace
and node. The site, the checker, the
error budget policy, scraping, traces and the canary smoke test were each
checked under the policies before they went into git.

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
