import json
import os
from datetime import datetime, timezone

import requests
from kubernetes import client, config
from kubernetes.client.rest import ApiException

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
    "Whoami demo": "https://whoami.smega.eu",
    "Developer tools": "https://tools.smega.eu",
    "SEO Checker": "https://seo.smega.eu",
    "One-time Secret": "https://yaots.smega.eu",
    "RunGuard": "https://runguard.smega.eu",
    "Nevnap API": "https://nevnapok.eu",
}


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


def check_one(name, url):
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
    except requests.RequestException:
        latency_ms = int((datetime.now(timezone.utc) - started).total_seconds() * 1000)
        ok = False
        status_code = None

    return {
        "name": name,
        "url": url,
        "ok": ok,
        "statusCode": status_code,
        "latencyMs": latency_ms,
        "lastCheckedAt": checked_at,
    }


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


def build_payload(existing, check=check_one):
    services = []
    for name, url in load_targets().items():
        result = check(name, url)
        prev = existing.get(name, {})
        history = prev.get("history", [])
        history.append({"t": result["lastCheckedAt"], "ok": result["ok"]})
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
    }


def main():
    config.load_incluster_config()
    v1 = client.CoreV1Api()

    existing = load_existing(v1)
    payload = build_payload(existing)

    body = {"data": {"status-data.json": json.dumps(payload, indent=2)}}
    v1.patch_namespaced_config_map(name=CONFIGMAP_NAME, namespace=NAMESPACE, body=body)
    print(f"patched ConfigMap {NAMESPACE}/{CONFIGMAP_NAME} with {len(payload['services'])} services")


if __name__ == "__main__":
    main()
