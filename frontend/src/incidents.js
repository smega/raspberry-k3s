// Incident log, newest first. Short on purpose: what broke, what fixed it,
// what changed afterwards. Longer write-ups get a `link`.
//
// impact: 'outage' (visitors saw it) or 'none' (found before it hurt anyone).
export const INCIDENTS = [
  {
    date: '2026-11-14',
    title: 'k3s upgrade 1.30 to 1.37: a short outage and an alert storm',
    impact: 'outage',
    duration: 'about 2 minutes, 2 sites',
    what:
      'I upgraded the cluster seven Kubernetes versions in a row, one node at a time. While the control plane restarted, the two sites served from the cluster returned 502 for about two minutes. Twice the status checker\'s own node restarted under it, and it recorded every site as down, including the ones on other servers. Those fake failures set off the SLO alerts for all eight services at once. That notification was too big for ntfy, which rejected it, and the rejection crashed Alertmanager through a bug in version 0.27. It restarted for about 40 minutes, re-sending the same alerts each time.',
    fix: 'Silenced the false alerts, upgraded Alertmanager to 0.28.1 (which has the fix), and capped how many alerts go into one notification.',
    lesson: 'The checker now skips a run in which no site answered at all, because that is its own network, not eight outages. Upgrades now silence only the alerts a rolling restart is expected to cause; the SLO alerts stay live.',
  },
  {
    date: '2026-11-13',
    title: 'Mail server certificate would have expired in December',
    impact: 'none',
    duration: 'caught 33 days before expiry',
    what:
      'While setting up certificate expiry monitoring, I listed the certificates on the VPS. Three Let\'s Encrypt certificates, including the mail server\'s, were due to expire in mid-December, and nothing was going to renew them: the renewal timer had been enabled but never started. There was a second problem behind it: even after a renewal, the mail server would have kept serving the old certificate, because nothing told it to reload.',
    fix: 'Started the renewal timer, tested the renewal against Let\'s Encrypt\'s staging environment, and added a hook that reloads the mail server after a renewal.',
    lesson: 'Every public certificate, the mail ports and the cluster\'s own certificates are now probed hourly, with an alert 14 days before expiry.',
  },
  {
    date: '2026-11-13',
    title: 'Critical alerts that had been firing for months',
    impact: 'none',
    duration: 'caught before it paged anyone',
    what:
      'Before sending alerts to my phone I checked what was already firing, and found three critical "component down" alerts that had been on the whole time. k3s runs the scheduler, controller-manager and kube-proxy inside its one process, so the default monitoring stack never found them. Two Services left behind by earlier monitoring installs also made Prometheus scrape every kubelet three times, which broke some of its rules.',
    fix: 'Turned off the monitors k3s has no targets for and deleted the leftover Services, before anything was routed to a phone.',
    lesson: 'Clean up the alert noise first. Paging on top of alerts that are always on teaches you to ignore the pager.',
  },
  {
    date: '2026-11-12',
    title: 'One failed check on RunGuard, cause unknown',
    impact: 'outage',
    duration: 'one 5-minute check',
    what:
      'The checker marked RunGuard down once, and the next check was fine again. By the time I looked, the Job logs were gone, and the checker had only stored "down", not why. RunGuard\'s own logs and nginx show no restart or error, so the cause can\'t be found any more.',
    fix: 'Nothing to fix by then. While looking, I did find that RunGuard deploys stopped the service for the whole binary upload; they now upload first and only restart for the swap.',
    lesson: 'The checker now stores the reason with every failed check (timeout, connection error or HTTP status), and you can see it by hovering over a red bar. The next one won\'t be a mystery.',
  },
  {
    date: '2026-11-12',
    title: 'All sites on the cluster down for a few minutes',
    impact: 'outage',
    duration: 'a few minutes',
    what:
      'I applied the Gateway manifest from the repo to the live cluster. That file is a template for people running their own copy, with a placeholder where the Gateway class name goes, and I applied it with the placeholder still in. Kong dropped the shared Gateway, and every public hostname on the cluster stopped answering.',
    fix: 'Re-applied the manifest with the real class name.',
    lesson: 'Hand-applied manifests are a risk. Moving the cluster to GitOps, so the live state always comes from reviewed config, is on the list.',
  },
  {
    date: '2026-09-18',
    title: 'Admin certificate expired, and nothing noticed',
    impact: 'none',
    duration: 'noticed 2 months later',
    what:
      'The client certificate my laptop uses to manage the cluster expired. Nothing watched it, so I only found out in November when kubectl stopped working. The cluster itself kept running.',
    fix: 'Worked through SSH on the control-plane node in the meantime, then issued a new kubeconfig.',
    lesson: 'Certificate expiry monitoring, for both the public TLS certificates and the cluster\'s own, is the next item on the list.',
  },
]
