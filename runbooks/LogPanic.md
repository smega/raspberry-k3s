# LogPanic, LogTraceback

**Severity:** warning (phone, normal priority)

## What fired

A log line, not a metric ( Loki's ruler, `observability/loki-rules.yaml` ):

- **LogPanic:** a line starting with `panic: ` or `fatal error: ` in any
  namespace: a Go program crashed.
- **LogTraceback:** a Python traceback in our own code ( the status
  checker in public-demo, pi-temp-led in cpu-led, anything Python in
  public-dashboard ).

Both look at the last 10 minutes, so they resolve on their own 10 minutes
after the last such line.

## Impact

Usually one crashed container that Kubernetes restarted. Repeats show up
as `KubePodCrashLooping` too; this alert carries the reason.

## Check

The stack, in Grafana Explore ( Loki ) or from the laptop:

```
{namespace="<ns>", pod="<pod>"} |~ "panic|fatal error|Traceback" 
kubectl -n <ns> logs <pod> --previous | tail -60
kubectl -n <ns> get pod <pod> -o jsonpath='{.status.containerStatuses[*].lastState}'
```

- In our own code: the stack names the file and line; fix it there.
- In a third-party controller: its version and the panic text are
  enough for an upstream issue search. If it keeps crashing, roll back
  the chart.
- `Exception ignored in ... __del__` at shutdown: noise, but noise that
  hides real ones. Fix it ( as pi-temp-led on 2026-11-23 ).

## History

- New on 2026-11-23. The 3 days of logs before had no panic; the only
  tracebacks were pi-temp-led's shutdown noise ( fixed the same day ).
