import json
import os
from datetime import datetime, timezone
from html.parser import HTMLParser
from urllib.parse import urljoin, urlsplit

import requests
from kubernetes import client, config
from kubernetes.client.rest import ApiException
from opentelemetry import trace
from opentelemetry.trace import Status, StatusCode

# One trace per run: a span for the run, one per site, and the HTTP request
# under it (requests instrumentation). Exported to Alloy (observability/)
# only when OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise the tracer is a
# no-op. A failed check's log line carries the trace id, so Grafana can go
# from that line to the trace.
tracer = trace.get_tracer("status-checker")


def init_tracing():
    if not os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT"):
        return None
    from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
    from opentelemetry.instrumentation.requests import RequestsInstrumentor
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import BatchSpanProcessor

    provider = TracerProvider()  # service.name from OTEL_SERVICE_NAME
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(provider)
    RequestsInstrumentor().instrument()
    return provider


def trace_suffix():
    ctx = trace.get_current_span().get_span_context()
    return f" trace_id={ctx.trace_id:032x}" if ctx.is_valid else ""

NAMESPACE = os.getenv("STATUS_NAMESPACE", "public-demo")
CONFIGMAP_NAME = os.getenv("STATUS_CONFIGMAP_NAME", "status-data")
HISTORY_LIMIT = int(os.getenv("STATUS_HISTORY_LIMIT", "288"))  # 24h at 5-minute checks
CHECK_TIMEOUT_SECONDS = float(os.getenv("STATUS_CHECK_TIMEOUT", "8"))
CHECK_INTERVAL_MINUTES = int(os.getenv("STATUS_CHECK_INTERVAL_MINUTES", "5"))  # must match the CronJob schedule
SLO_PERCENT = float(os.getenv("STATUS_SLO_PERCENT", "99.5"))
SLO_WINDOW_DAYS = int(os.getenv("STATUS_SLO_WINDOW_DAYS", "30"))

# name -> url. Override entirely with STATUS_TARGETS_JSON (a JSON object of
# the same shape) if you want a different set without rebuilding the image.
DEFAULT_TARGETS = {
    "Personal site": "https://smega.eu",
    "Cluster dashboard": "https://cluster.smega.eu",
    "Developer tools": "https://tools.smega.eu",
    "SEO Checker": "https://seo.smega.eu",
    "One-time Secret": "https://yaots.smega.eu",
    "RunGuard": "https://runguard.smega.eu",
    "Nevnap API": "https://nevnapok.eu",
}


# The watchers outside the cluster (both on the manage VPS) report to RunGuard;
# their public RunGuard pages (https://runguard.smega.eu/p/<token>) come from
# the hand-made Secret runguard-public-jobs. Only status and times go into the
# payload, never the token.
WATCHERS = {
    "externalProbe": "RUNGUARD_EXTERNAL_PROBE_URL",  # outside-in probe, every 2 min
    "restoreTest": "RUNGUARD_RESTORE_TEST_URL",  # backup restore test, weekly
}


def fetch_watchers():
    out = {}
    for key, env in WATCHERS.items():
        page = os.getenv(env, "").strip().rstrip("/")
        if not page:
            continue
        base, token = page.rsplit("/p/", 1)
        try:
            resp = requests.get(f"{base}/api/public/job/{token}", timeout=CHECK_TIMEOUT_SECONDS)
            resp.raise_for_status()
            job = resp.json()
        except (requests.RequestException, ValueError) as e:
            print(f"watcher {key}: {type(e).__name__}", flush=True)
            continue
        last = job.get("last_run") or {}
        out[key] = {
            "onTime": job.get("status") == "ok",
            "lastStatus": last.get("status"),
            "lastAt": last.get("at"),
        }
    return out


def load_targets():
    raw = os.getenv("STATUS_TARGETS_JSON")
    if not raw:
        return DEFAULT_TARGETS
    try:
        parsed = json.loads(raw)
        if isinstance(parsed, dict) and parsed:
            return parsed
    except (TypeError, ValueError):
        pass
    return DEFAULT_TARGETS


class _AssetLinks(HTMLParser):
    """Collects <script src> and <link rel=stylesheet href> from a page."""

    def __init__(self):
        super().__init__()
        self.links = []

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == "script" and a.get("src"):
            self.links.append(a["src"])
        elif tag == "link" and "stylesheet" in (a.get("rel") or "").split() and a.get("href"):
            self.links.append(a["href"])


def missing_asset(resp):
    """The first script or stylesheet the page can't load, as "asset HTTP 404".

    A single-page app answers 200 with its index.html even when its bundle
    is gone, and the page is then blank. On 2026-11-21 a 404 for the status
    page's new bundle stayed at Cloudflare for 4 hours while every check of
    the page itself passed. Only files on the page's own host are checked:
    a third-party CDN being down is not this service being down.
    """
    if "text/html" not in resp.headers.get("Content-Type", ""):
        return None
    parser = _AssetLinks()
    try:
        parser.feed(resp.text)
    except Exception:
        return None
    host = urlsplit(resp.url).netloc
    for link in dict.fromkeys(parser.links):
        url = urljoin(resp.url, link)
        if urlsplit(url).netloc != host:
            continue
        try:
            r = requests.get(url, timeout=CHECK_TIMEOUT_SECONDS)
        except requests.RequestException as e:
            return f"asset {type(e).__name__}"
        if r.status_code >= 400:
            return f"asset HTTP {r.status_code}"
    return None


def _check_one(name, url):
    checked_at = datetime.now(timezone.utc).isoformat()
    started = datetime.now(timezone.utc)
    try:
        resp = requests.get(url, timeout=CHECK_TIMEOUT_SECONDS, allow_redirects=True)
        latency_ms = int((datetime.now(timezone.utc) - started).total_seconds() * 1000)
        # Any response under 500 means the server is up and handling requests
        # correctly -- a login-walled service returning 401/403 to an
        # anonymous check is working as intended, not down.
        ok = resp.status_code < 500
        status_code = resp.status_code
        error = None if ok else f"HTTP {status_code}"
        if ok and resp.status_code < 300:
            error = missing_asset(resp)
            ok = error is None
    except requests.RequestException as e:
        latency_ms = int((datetime.now(timezone.utc) - started).total_seconds() * 1000)
        ok = False
        status_code = None
        # Class name only (ReadTimeout, ConnectionError, SSLError...): enough
        # to tell a slow service from a dead one, without leaking URLs or
        # stack details onto a public page. A ConnectionError also gets
        # urllib3's reason (NameResolutionError, NewConnectionError,
        # ConnectTimeoutError...), so DNS can be told from a refused or
        # dropped connection.
        error = type(e).__name__
        reason = getattr(e.args[0], "reason", None) if e.args else None
        if reason is not None:
            error = f"{error} ({type(reason).__name__})"

    if error:
        print(f"{name}: down ({error}, {latency_ms} ms){trace_suffix()}", flush=True)

    result = {
        "name": name,
        "url": url,
        "ok": ok,
        "statusCode": status_code,
        "latencyMs": latency_ms,
        "lastCheckedAt": checked_at,
    }
    if error:
        result["error"] = error
    return result


def check_one(name, url):
    with tracer.start_as_current_span(f"check {name}", attributes={"url.full": url}) as span:
        result = _check_one(name, url)
        span.set_attribute("check.ok", result["ok"])
        if result.get("error"):
            span.set_status(Status(StatusCode.ERROR, result["error"]))
        return result


def load_existing(v1):
    """Returns {name: {"history": [...], "daily": [...]}} from the last run."""
    try:
        cm = v1.read_namespaced_config_map(name=CONFIGMAP_NAME, namespace=NAMESPACE)
        raw = (cm.data or {}).get("status-data.json")
        if not raw:
            return {}
        return existing_from_payload(json.loads(raw))
    except (ApiException, ValueError, KeyError):
        return {}


def existing_from_payload(parsed):
    out = {}
    for s in parsed.get("services", []):
        history = s.get("history", [])
        daily = s.get("daily")
        if daily is None:
            # First run with the daily rollup: seed it from the 24h history
            # so the SLO doesn't start from zero.
            daily = rollup_from_history(history)
        out[s["name"]] = {"history": history, "daily": daily}
    return out


def rollup_from_history(history):
    days = {}
    for h in history:
        date = h["t"][:10]
        bucket = days.setdefault(date, {"date": date, "checks": 0, "up": 0})
        bucket["checks"] += 1
        bucket["up"] += 1 if h.get("ok") else 0
    return [days[d] for d in sorted(days)]


def add_to_daily(daily, checked_at, ok):
    """Counts one check into its UTC day and keeps the last SLO_WINDOW_DAYS days."""
    date = checked_at[:10]
    if not daily or daily[-1]["date"] != date:
        daily.append({"date": date, "checks": 0, "up": 0})
    daily[-1]["checks"] += 1
    daily[-1]["up"] += 1 if ok else 0
    return daily[-SLO_WINDOW_DAYS:]


def slo_summary(daily):
    """Availability and error budget over the rolling window.

    The budget is sized for the whole window (30 days of checks), so a
    service with only a few days of data hasn't "earned" a smaller budget:
    each failed check simply spends CHECK_INTERVAL_MINUTES of it.
    """
    checks = sum(d["checks"] for d in daily)
    up = sum(d["up"] for d in daily)
    failed = checks - up
    budget_minutes = SLO_WINDOW_DAYS * 24 * 60 * (100 - SLO_PERCENT) / 100
    used_minutes = failed * CHECK_INTERVAL_MINUTES
    return {
        "targetPercent": SLO_PERCENT,
        "windowDays": SLO_WINDOW_DAYS,
        "daysOfData": len(daily),
        "uptimePercent": round(up / checks * 100, 3) if checks else None,
        "budgetMinutes": round(budget_minutes, 1),
        "budgetUsedMinutes": used_minutes,
        "budgetRemainingPercent": round(max(0.0, 100 * (1 - used_minutes / budget_minutes)), 1)
        if budget_minutes > 0
        else None,
    }


def uptime_percent(history):
    if not history:
        return None
    up = sum(1 for h in history if h.get("ok"))
    return round(up / len(history) * 100, 2)


class CheckerOffline(Exception):
    """Every target failed before getting any HTTP response."""


def build_payload(existing, check=check_one):
    results = [check(name, url) for name, url in load_targets().items()]
    # The targets are independent sites on different hosts (the cluster, the
    # VPS, Cloudflare). If none of them even answered, the likely cause is
    # this pod's own network (e.g. its node restarting k3s, which resets
    # iptables), not eight simultaneous outages. Recording that would burn
    # every service's error budget for nothing, so the run is skipped.
    # Seen twice during the 2026-11-14 k3s upgrade.
    if results and all(not r["ok"] and r["statusCode"] is None for r in results):
        raise CheckerOffline(", ".join(f"{r['name']}: {r.get('error')}" for r in results))

    services = []
    for result in results:
        name = result["name"]
        prev = existing.get(name, {})
        history = prev.get("history", [])
        entry = {"t": result["lastCheckedAt"], "ok": result["ok"]}
        if result.get("error"):
            # Kept with the check, so a failure can still be explained after
            # the Job's logs are gone.
            entry["error"] = result["error"]
        history.append(entry)
        history = history[-HISTORY_LIMIT:]
        daily = add_to_daily(prev.get("daily", []), result["lastCheckedAt"], result["ok"])

        result["uptimePercent"] = uptime_percent(history)
        result["slo"] = slo_summary(daily)
        result["history"] = history
        result["daily"] = daily
        services.append(result)

    return {
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "services": services,
        "watchers": fetch_watchers(),
    }


def main():
    provider = init_tracing()
    try:
        with tracer.start_as_current_span("checker run"):
            run()
    finally:
        # A Job's pod exits right after: send the spans before it does.
        if provider:
            provider.shutdown()


def run():
    config.load_incluster_config()
    v1 = client.CoreV1Api()

    existing = load_existing(v1)
    try:
        payload = build_payload(existing)
    except CheckerOffline as e:
        # Not patching leaves generatedAt as it was, so if this keeps
        # happening StatusCheckerStale fires instead of fake outages.
        print(f"no target answered at all, assuming this pod's network is down; run not recorded ({e}){trace_suffix()}")
        return

    body = {"data": {"status-data.json": json.dumps(payload, indent=2)}}
    v1.patch_namespaced_config_map(name=CONFIGMAP_NAME, namespace=NAMESPACE, body=body)
    print(f"patched ConfigMap {NAMESPACE}/{CONFIGMAP_NAME} with {len(payload['services'])} services{trace_suffix()}")


if __name__ == "__main__":
    main()
