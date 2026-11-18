# content/

Text that status.smega.eu loads at runtime instead of bundling it into the
image. Change a file here, commit, push: Flux updates the `status-content`
ConfigMap (source polled every 5 min), and the kubelet replaces the
mounted files in the running status-web pods about a minute later. No
image build, no restart. nginx serves `/content/` with `Cache-Control:
no-cache`, so neither Cloudflare nor the browser keeps an old copy.

- `incidents.json` -- the incident log (Incidents tab), newest first.
  Short on purpose: what broke, what fixed it, what changed afterwards.
  Fields: `date`, `title`, `impact` (`outage` if visitors saw it, `none`
  if it was found before it hurt anyone), `duration`, `what`, `fix`,
  `lesson`, and optionally `link` to a longer write-up (e.g. in
  `postmortems/`).

A ConfigMap holds at most 1 MiB, plenty for text; images belong elsewhere.
Check a change before pushing: `python3 -m json.tool incidents.json`.
