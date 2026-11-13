import { h, render } from 'preact'
import { useEffect, useMemo, useState } from 'preact/hooks'

// Status palette (fixed, never themed) - good/critical only, this page has
// no intermediate "degraded" state.
const GOOD = '#0ca30c'
const CRITICAL = '#d03b3b'

const SURFACE = '#fcfcfb'
const PAGE = '#f9f9f7'
const INK_PRIMARY = '#0b0b0b'
const INK_SECONDARY = '#52514e'
const INK_MUTED = '#898781'
const BORDER = 'rgba(11,11,11,0.10)'

function Card({ children }) {
  return (
    <div
      style={{
        background: SURFACE,
        border: `1px solid ${BORDER}`,
        borderRadius: '12px',
        padding: '20px',
      }}
    >
      {children}
    </div>
  )
}

// Caps the strip at a fixed number of bars regardless of how many raw
// checks came back, so the strip always fits its card instead of
// overflowing into the next one. A bucket is "down" if any check inside
// it was down.
const HISTORY_BARS = 48

function downsample(history) {
  if (history.length <= HISTORY_BARS) return history
  const bucketSize = history.length / HISTORY_BARS
  const bars = []
  for (let i = 0; i < HISTORY_BARS; i++) {
    const start = Math.floor(i * bucketSize)
    const end = Math.floor((i + 1) * bucketSize) || start + 1
    const bucket = history.slice(start, end)
    bars.push({ t: bucket[bucket.length - 1].t, ok: bucket.every((h) => h.ok) })
  }
  return bars
}

function HistoryStrip({ history }) {
  if (!history || history.length === 0) {
    return <div style={{ fontSize: '12px', color: INK_MUTED }}>No checks recorded yet.</div>
  }
  const bars = downsample(history)
  return (
    <div style={{ display: 'flex', gap: '2px', height: '22px', width: '100%' }}>
      {bars.map((h, i) => (
        <div
          key={i}
          title={`${h.t}: ${h.ok ? 'up' : 'down'}`}
          style={{
            flex: '1 1 0',
            minWidth: 0,
            height: '100%',
            borderRadius: '2px',
            background: h.ok ? GOOD : CRITICAL,
          }}
        />
      ))}
    </div>
  )
}

// The budget is sized for the full window, so the 30-day uptime is only
// shown once there are 30 days of data; before that it would look like a
// missed SLO after a single failed check.
function ErrorBudget({ slo }) {
  const left = slo.budgetRemainingPercent ?? 0
  const exhausted = left <= 0
  const minutesLeft = Math.max(0, slo.budgetMinutes - slo.budgetUsedMinutes)
  const windowFull = slo.daysOfData >= slo.windowDays
  return (
    <div style={{ marginTop: '14px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: '8px', fontSize: '12px', color: INK_SECONDARY, marginBottom: '4px' }}>
        <span>Error budget</span>
        <span style={{ color: exhausted ? CRITICAL : INK_SECONDARY }}>
          {left.toFixed(0)}% left
          <span style={{ color: INK_MUTED }}>
            {' '}
            · {Math.round(minutesLeft)} of {Math.round(slo.budgetMinutes)} min
          </span>
        </span>
      </div>
      <div style={{ height: '6px', borderRadius: '3px', background: BORDER, overflow: 'hidden' }}>
        <div style={{ width: `${Math.min(100, left)}%`, height: '100%', background: exhausted ? CRITICAL : INK_SECONDARY, borderRadius: '3px' }} />
      </div>
      <div style={{ fontSize: '11px', color: INK_MUTED, marginTop: '4px' }}>
        SLO {slo.targetPercent}% over {slo.windowDays} days ·{' '}
        {windowFull && slo.uptimePercent != null
          ? `${slo.uptimePercent}% actual`
          : `${slo.daysOfData} of ${slo.windowDays} days of data so far`}
      </div>
    </div>
  )
}

function ServiceCard({ s }) {
  const statusColor = s.ok ? GOOD : CRITICAL
  const statusLabel = s.ok ? 'Operational' : 'Down'

  return (
    <Card>
      <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: '10px', marginBottom: '8px' }}>
        <a
          href={s.url}
          target="_blank"
          rel="noreferrer"
          style={{ fontWeight: 600, fontSize: '15px', color: INK_PRIMARY, textDecoration: 'none' }}
        >
          {s.name}
        </a>
        {/* Identity comes from the dot, not the text color - text stays a plain ink token. */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '13px', color: INK_SECONDARY, whiteSpace: 'nowrap' }}>
          <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: statusColor, display: 'inline-block' }} />
          {statusLabel}
        </div>
      </div>

      <div style={{ fontSize: '12px', color: INK_MUTED, marginBottom: '12px' }}>
        {s.uptimePercent != null ? `${s.uptimePercent}% uptime (24h)` : 'Collecting data…'}
        {s.latencyMs != null && ` · ${s.latencyMs}ms`}
      </div>

      <HistoryStrip history={s.history} />

      {s.slo && <ErrorBudget slo={s.slo} />}
    </Card>
  )
}

// Same scale as the RGB LED on each Pi (pi-temp-led, LED_SOURCE=load):
// blue when idle, red at full CPU load, blended on a square-root curve so the
// 1-10% these Pis usually sit at still shows as visible color. Endpoints are
// softened versions of the LED's pure blue/red so they read on a light page.
const LED_COLD = [42, 111, 219]
const LED_HOT = [208, 59, 59]

function ledColor(cpuPercent) {
  const f = Math.sqrt(Math.min(1, Math.max(0, cpuPercent / 100)))
  const [r, g, b] = LED_COLD.map((v, i) => Math.round(v + (LED_HOT[i] - v) * f))
  return `rgb(${r}, ${g}, ${b})`
}

function formatUptime(seconds) {
  if (seconds == null) return '-'
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  if (d > 0) return `${d}d ${h}h`
  const m = Math.floor((seconds % 3600) / 60)
  return `${h}h ${m}m`
}

function formatGiB(bytes) {
  return (bytes / 1024 ** 3).toFixed(1)
}

// Each node card ends with all three 24h charts stacked, sharing one
// "24h ago ... now" axis line underneath.
const CHARTS = [
  { key: 'tempHistory', label: 'Temperature', unit: ' °C' },
  { key: 'cpuHistory', label: 'CPU', unit: '%' },
  { key: 'memHistory', label: 'Memory', unit: '%' },
]

function Sparkline({ label, points, unit }) {
  const header = (range) => (
    <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '11px', color: INK_MUTED, marginBottom: '2px' }}>
      <span>{label}</span>
      <span>{range}</span>
    </div>
  )
  if (!points || points.length < 2) {
    return (
      <div style={{ marginBottom: '8px' }}>
        {header('no history yet')}
      </div>
    )
  }
  const W = 240
  const H = 26
  const vals = points.map((p) => p.v)
  const lo = Math.min(...vals)
  const hi = Math.max(...vals)
  const span = hi - lo || 1
  const d = points
    .map((p, i) => {
      const x = (i / (points.length - 1)) * W
      const y = H - 2 - ((p.v - lo) / span) * (H - 4)
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(' ')
  return (
    <div style={{ marginBottom: '8px' }}>
      {header(`${lo.toFixed(1)}-${hi.toFixed(1)}${unit}`)}
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" style={{ width: '100%', height: `${H}px`, display: 'block' }}>
        <path d={d} fill="none" stroke={INK_SECONDARY} stroke-width="1.5" vector-effect="non-scaling-stroke" />
      </svg>
    </div>
  )
}

function Meter({ label, percent, detail }) {
  return (
    <div style={{ marginBottom: '10px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '12px', color: INK_SECONDARY, marginBottom: '4px' }}>
        <span>{label}</span>
        <span>
          {percent != null ? `${percent.toFixed(0)}%` : '-'}
          {detail && <span style={{ color: INK_MUTED }}> · {detail}</span>}
        </span>
      </div>
      <div style={{ height: '6px', borderRadius: '3px', background: BORDER, overflow: 'hidden' }}>
        <div style={{ width: `${Math.min(100, percent ?? 0)}%`, height: '100%', background: INK_SECONDARY, borderRadius: '3px' }} />
      </div>
    </div>
  )
}

function nodeState(n) {
  if (!n.reachable) return { color: CRITICAL, label: 'Unreachable' }
  if (!n.ready) return { color: CRITICAL, label: 'Not ready' }
  return { color: GOOD, label: 'Ready' }
}

function NodeCard({ n }) {
  const state = nodeState(n)
  return (
    <Card>
      <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between', gap: '10px', marginBottom: '10px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontWeight: 600, fontSize: '15px', color: INK_PRIMARY }}>
          <span
            title="The color this Pi's RGB LED is showing (CPU load)"
            style={{
              width: '12px',
              height: '12px',
              borderRadius: '50%',
              flexShrink: 0,
              display: 'inline-block',
              background: n.cpuPercent != null ? ledColor(n.cpuPercent) : INK_MUTED,
              boxShadow: n.cpuPercent != null ? `0 0 6px ${ledColor(n.cpuPercent)}` : 'none',
            }}
          />
          {n.name}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '13px', color: INK_SECONDARY, whiteSpace: 'nowrap' }}>
          <span style={{ width: '8px', height: '8px', borderRadius: '50%', background: state.color, display: 'inline-block' }} />
          {state.label}
        </div>
      </div>

      {!n.reachable && (
        <div style={{ fontSize: '12px', color: INK_MUTED, marginBottom: '10px' }}>
          No live readings: this Pi is not answering. The charts show its last 24 hours.
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'baseline', gap: '8px', marginBottom: '14px' }}>
        <span style={{ fontSize: '24px', fontWeight: 600, color: INK_PRIMARY }}>
          {n.tempC != null ? `${n.tempC.toFixed(1)} °C` : '-'}
        </span>
        <span style={{ fontSize: '12px', color: INK_MUTED }}>CPU temp</span>
      </div>

      <Meter label="CPU" percent={n.cpuPercent} />
      <Meter
        label="Memory"
        percent={n.memPercent}
        detail={n.memUsedBytes != null && n.memTotalBytes != null ? `${formatGiB(n.memUsedBytes)} / ${formatGiB(n.memTotalBytes)} GiB` : null}
      />

      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '12px', color: INK_MUTED, margin: '4px 0 14px' }}>
        <span>Load {n.load1 != null ? n.load1.toFixed(2) : '-'}</span>
        <span>Up {formatUptime(n.uptimeSeconds)}</span>
      </div>

      {CHARTS.map((c) => (
        <Sparkline key={c.key} label={c.label} points={n[c.key]} unit={c.unit} />
      ))}
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '11px', color: INK_MUTED }}>
        <span>24h ago</span>
        <span>now</span>
      </div>
    </Card>
  )
}

// Real photo of the cluster (cropped, EXIF/GPS stripped, WebP). The
// thumbnail links to the larger version.
function ClusterPhoto() {
  return (
    <figure style={{ margin: 0, flex: '0 0 auto', width: '180px', maxWidth: '100%' }}>
      <a href="/photos/cluster.webp" target="_blank" rel="noreferrer">
        <img
          src="/photos/cluster-small.webp"
          alt="The four-Pi cluster on a shelf, each RGB LED glowing purple"
          width="180"
          height="226"
          loading="lazy"
          style={{ display: 'block', width: '100%', height: 'auto', borderRadius: '10px', border: `1px solid ${BORDER}` }}
        />
      </a>
      <figcaption style={{ fontSize: '12px', color: INK_MUTED, marginTop: '6px' }}>
        The actual cluster. Each LED follows its Pi's CPU load.
      </figcaption>
    </figure>
  )
}

function NodesSection({ apiBase }) {
  const [nodes, setNodes] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const load = () => {
      fetch(`${apiBase}/api/nodes`)
        .then((r) => {
          if (!r.ok) throw new Error(`HTTP ${r.status}`)
          return r.json()
        })
        .then((d) => {
          setNodes(d)
          setError('')
        })
        .catch((err) => setError(err.message))
    }
    load()
    const interval = setInterval(load, 15_000)
    return () => clearInterval(interval)
  }, [apiBase])

  return (
    <section>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: '20px 32px', alignItems: 'flex-start', marginBottom: '20px' }}>
        <p style={{ flex: '1 1 320px', margin: 0, color: INK_SECONDARY, lineHeight: 1.6, maxWidth: '560px' }}>
          The four Raspberry Pis that run this page and its checks, as a k3s Kubernetes cluster on a shelf at
          home. Live readings from the cluster's own Prometheus, refreshed every 15 seconds. The dot next to each
          Pi's name is the color its RGB LED shows: blue when idle, turning red with CPU load.
        </p>
        <ClusterPhoto />
      </div>

      {error && !nodes && <div style={{ fontSize: '14px', color: INK_SECONDARY }}>Node stats unavailable: {error}</div>}
      {!nodes && !error && <div style={{ color: INK_SECONDARY }}>Loading…</div>}

      {nodes && (
        <>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))',
              gap: '14px',
              marginBottom: '16px',
            }}
          >
            {nodes.nodes.map((n) => (
              <NodeCard key={n.name} n={n} />
            ))}
          </div>
          <div style={{ fontSize: '12px', color: INK_MUTED }}>
            Updated: {nodes.generatedAt}
            {error && ` · last refresh failed (${error})`}
          </div>
        </>
      )}
    </section>
  )
}

// Architecture diagram for the "How this page works" section. Plain SVG in a
// fixed coordinate system; on narrow screens its container scrolls sideways
// instead of shrinking the text below readable size.
const DIAGRAM_COLS = [10, 200, 390, 590]
const BOX_W = 160
const BOX_H = 46

function DiagramBox({ col, y, title, sub }) {
  const x = DIAGRAM_COLS[col]
  return (
    <g>
      <rect x={x} y={y} width={BOX_W} height={BOX_H} rx="8" fill={SURFACE} stroke={INK_MUTED} stroke-width="1" />
      <text x={x + BOX_W / 2} y={y + 19} text-anchor="middle" font-size="13" font-weight="600" fill={INK_PRIMARY}>
        {title}
      </text>
      <text x={x + BOX_W / 2} y={y + 35} text-anchor="middle" font-size="11" fill={INK_SECONDARY}>
        {sub}
      </text>
    </g>
  )
}

function DiagramArrow({ x1, y1, x2, y2, label, lx, ly, anchor = 'middle' }) {
  return (
    <g>
      <line x1={x1} y1={y1} x2={x2} y2={y2} stroke={INK_SECONDARY} stroke-width="1.2" marker-end="url(#arrow)" />
      {label && (
        <text x={lx} y={ly} text-anchor={anchor} font-size="11" fill={INK_MUTED}>
          {label}
        </text>
      )}
    </g>
  )
}

function ArchitectureDiagram() {
  // Box edges, for readable arrow coordinates.
  const cx = (col) => DIAGRAM_COLS[col] + BOX_W / 2
  const right = (col) => DIAGRAM_COLS[col] + BOX_W
  const left = (col) => DIAGRAM_COLS[col]
  const ROW = [20, 125, 215, 305, 390]

  return (
    <div style={{ overflowX: 'auto' }}>
      <svg
        viewBox="0 0 760 450"
        role="img"
        aria-label="Architecture: browser to Cloudflare to nginx on a VPS, over Tailscale to Kong on the k3s cluster, which routes to status-web and status-api. A CronJob writes check results into a ConfigMap mounted by status-api; Prometheus feeds the node readings."
        style={{ width: '100%', minWidth: '640px', display: 'block' }}
      >
        <defs>
          <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" fill={INK_SECONDARY} />
          </marker>
        </defs>

        {/* Cluster boundary */}
        <rect x="185" y="95" width="570" height="350" rx="12" fill="none" stroke={INK_MUTED} stroke-dasharray="5 4" />
        <text x="200" y="114" font-size="11" fill={INK_MUTED}>
          k3s cluster · 4 × Raspberry Pi
        </text>

        <DiagramBox col={0} y={ROW[0]} title="Browser" sub="you" />
        <DiagramBox col={1} y={ROW[0]} title="Cloudflare" sub="DNS, TLS, cache" />
        <DiagramBox col={2} y={ROW[0]} title="nginx" sub="on a small VPS" />
        <DiagramArrow x1={right(0)} y1={ROW[0] + 23} x2={left(1)} y2={ROW[0] + 23} />
        <DiagramArrow x1={right(1)} y1={ROW[0] + 23} x2={left(2)} y2={ROW[0] + 23} />

        <DiagramBox col={2} y={ROW[1]} title="Kong" sub="Gateway API" />
        <DiagramArrow x1={cx(2)} y1={ROW[0] + BOX_H} x2={cx(2)} y2={ROW[1]} label="Tailscale" lx={cx(2) + 8} ly={ROW[0] + 72} anchor="start" />

        <DiagramBox col={1} y={ROW[2]} title="status-web" sub="Preact · status.smega.eu" />
        <DiagramBox col={3} y={ROW[2]} title="status-api" sub="Go · api-status.smega.eu" />
        <DiagramArrow x1={left(2) + 20} y1={ROW[1] + BOX_H} x2={cx(1) + 30} y2={ROW[2]} />
        <DiagramArrow x1={right(2) - 20} y1={ROW[1] + BOX_H} x2={cx(3) - 30} y2={ROW[2]} />

        <DiagramBox col={0} y={ROW[3]} title="8 public services" sub="checked every 5 min" />
        <DiagramBox col={1} y={ROW[3]} title="status-checker" sub="CronJob, Python" />
        <DiagramBox col={2} y={ROW[3]} title="ConfigMap" sub="last 24h of results" />
        <DiagramBox col={3} y={ROW[3]} title="Prometheus" sub="kube-prometheus-stack" />
        <DiagramArrow x1={left(1)} y1={ROW[3] + 23} x2={right(0)} y2={ROW[3] + 23} />
        <DiagramArrow x1={right(1)} y1={ROW[3] + 23} x2={left(2)} y2={ROW[3] + 23} />
        <DiagramArrow x1={right(2) - 20} y1={ROW[3]} x2={left(3) + 20} y2={ROW[2] + BOX_H} label="mounted" lx={right(2) + 6} ly={ROW[3] - 22} anchor="middle" />
        <DiagramArrow x1={cx(3)} y1={ROW[3]} x2={cx(3)} y2={ROW[2] + BOX_H} label="PromQL" lx={cx(3) + 8} ly={ROW[3] - 18} anchor="start" />

        <DiagramBox col={1} y={ROW[4]} title="pi-temp-led" sub="LED shows CPU load" />
        <DiagramBox col={3} y={ROW[4]} title="node-exporter" sub="+ kube-state-metrics" />
        <DiagramArrow x1={cx(3)} y1={ROW[4]} x2={cx(3)} y2={ROW[3] + BOX_H} label="scrape" lx={cx(3) + 8} ly={ROW[4] - 18} anchor="start" />
      </svg>
    </div>
  )
}

// Badge slugs must match backend/badge.go's slugify().
function slugify(name) {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function BadgesSection({ apiBase, services }) {
  const [copied, setCopied] = useState('')
  const copy = (slug, text) => {
    navigator.clipboard?.writeText(text).then(() => {
      setCopied(slug)
      setTimeout(() => setCopied(''), 1500)
    })
  }
  return (
    <details style={{ marginBottom: '24px' }}>
      <summary style={{ cursor: 'pointer', fontSize: '13px', color: INK_SECONDARY }}>Uptime badges for READMEs</summary>
      <div style={{ marginTop: '12px', display: 'grid', gap: '10px' }}>
        {services.map((s) => {
          const slug = slugify(s.name)
          const md = `[![uptime](${apiBase}/badge/${slug}.svg)](https://status.smega.eu)`
          return (
            <div key={slug} style={{ display: 'flex', alignItems: 'center', gap: '12px', flexWrap: 'wrap', fontSize: '12px' }}>
              <span style={{ width: '140px', color: INK_PRIMARY }}>{s.name}</span>
              <span style={{ width: '110px' }}>
                <img src={`${apiBase}/badge/${slug}.svg`} alt={`${s.name} uptime`} height="20" style={{ display: 'block' }} />
              </span>
              <code
                style={{
                  flex: '1 1 320px',
                  minWidth: 0,
                  overflowX: 'auto',
                  whiteSpace: 'nowrap',
                  padding: '4px 8px',
                  borderRadius: '6px',
                  background: SURFACE,
                  border: `1px solid ${BORDER}`,
                  color: INK_SECONDARY,
                }}
              >
                {md}
              </code>
              <button
                type="button"
                onClick={() => copy(slug, md)}
                style={{
                  border: `1px solid ${BORDER}`,
                  borderRadius: '6px',
                  background: SURFACE,
                  padding: '4px 10px',
                  fontSize: '12px',
                  cursor: 'pointer',
                  color: INK_SECONDARY,
                }}
              >
                {copied === slug ? 'Copied' : 'Copy'}
              </button>
            </div>
          )
        })}
      </div>
    </details>
  )
}

// Baked in at image build time by scripts/publish-status.sh (Docker build
// args -> VITE_* env), so the page can say which commit it is running.
const BUILD = {
  sha: import.meta.env.VITE_BUILD_SHA,
  time: import.meta.env.VITE_BUILD_TIME,
  message: import.meta.env.VITE_BUILD_MESSAGE,
}

function formatBuildTime(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('en-GB', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'UTC',
    timeZoneName: 'short',
  })
}

function LastDeploy() {
  if (!BUILD.sha || !BUILD.time) return null
  return (
    <div style={{ marginTop: '16px', paddingTop: '12px', borderTop: `1px solid ${BORDER}`, fontSize: '12px', color: INK_MUTED }}>
      Last deploy: {formatBuildTime(BUILD.time)} · <code>{BUILD.sha}</code>
      {BUILD.message && <> · {BUILD.message}</>}
    </div>
  )
}

function HowItWorksSection() {
  const p = { margin: '0 0 12px', fontSize: '14px', color: INK_SECONDARY, lineHeight: 1.6 }
  return (
    <section style={{ marginTop: '48px' }}>
      <h2 style={{ fontSize: '20px', fontWeight: 600, margin: '0 0 16px', color: INK_PRIMARY }}>How this page works</h2>
      <Card>
        <ArchitectureDiagram />
        <div style={{ marginTop: '20px', maxWidth: '720px' }}>
          <p style={p}>
            <strong style={{ color: INK_PRIMARY }}>No database.</strong> A Python CronJob checks each service every 5
            minutes and writes the last 24 hours of results into a ConfigMap. The Go API mounts that ConfigMap
            read-only and serves it as-is, so there is nothing to back up or migrate.
          </p>
          <p style={p}>
            <strong style={{ color: INK_PRIMARY }}>No new agent for the node readings.</strong> The cluster already runs
            Prometheus with node-exporter on every Pi, so the API sends a few fixed PromQL queries and caches the
            answer for 15 seconds. Nodes show up as "Pi #N" rather than hostnames or IPs.
          </p>
          <p style={{ ...p, margin: 0 }}>
            <strong style={{ color: INK_PRIMARY }}>Standard Kubernetes routing.</strong> Requests come through
            Cloudflare to nginx on a small VPS, then over Tailscale to Kong, which implements the Gateway API in front
            of the apps. Images are built for arm64 and pulled from a private registry.
          </p>
          <p style={{ ...p, margin: '16px 0 0' }}>
            Source code, manifests and the full setup:{' '}
            <a href="https://github.com/smega/raspberry-k3s" target="_blank" rel="noreferrer" style={{ color: INK_PRIMARY, fontWeight: 500 }}>
              github.com/smega/raspberry-k3s
            </a>
          </p>
        </div>
        <LastDeploy />
      </Card>
    </section>
  )
}

// Same Buttondown list and links as smega.eu. The embed form is a plain
// HTML POST; the hidden tag marks subscribers who came from this page.
const BUTTONDOWN_USER = 'smega'
const AUTHOR_LINKS = [
  { label: 'smega.eu', href: 'https://smega.eu' },
  { label: 'GitHub', href: 'https://github.com/smega' },
  { label: 'LinkedIn', href: 'https://www.linkedin.com/in/szabolcs-mega-822b6a63/' },
  { label: 'Cluster dashboard', href: 'https://cluster.smega.eu' },
]

function AuthorSection() {
  return (
    <section style={{ marginTop: '48px' }}>
      <Card>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '20px 32px', alignItems: 'center', justifyContent: 'space-between' }}>
          <div style={{ flex: '1 1 320px', minWidth: 0 }}>
            <div style={{ fontWeight: 600, fontSize: '16px', color: INK_PRIMARY, marginBottom: '6px' }}>
              Built and run by Szabolcs Mega
            </div>
            <p style={{ margin: '0 0 12px', fontSize: '14px', color: INK_SECONDARY, lineHeight: 1.6 }}>
              Senior SRE / DevOps engineer in Bristol. This cluster is my home lab; at work I've spent years
              running infrastructure as a team of one. I write about it: costs, migrations, incidents, and AI
              tools in day-to-day ops.
            </p>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: '6px 16px', fontSize: '13px' }}>
              {AUTHOR_LINKS.map((l) => (
                <a key={l.href} href={l.href} target="_blank" rel="noreferrer" style={{ color: INK_PRIMARY }}>
                  {l.label}
                </a>
              ))}
            </div>
          </div>

          <form
            action={`https://buttondown.com/api/emails/embed-subscribe/${BUTTONDOWN_USER}`}
            method="post"
            style={{ flex: '1 1 280px', maxWidth: '400px', minWidth: 0 }}
          >
            <div style={{ fontSize: '13px', color: INK_SECONDARY, marginBottom: '8px' }}>
              A practical post every couple of weeks, no fluff.
            </div>
            <input type="hidden" name="tag" value="status-page" />
            <div style={{ display: 'flex', gap: '8px' }}>
              <input
                type="email"
                name="email"
                required
                placeholder="you@company.com"
                aria-label="Email address"
                style={{
                  flex: '1 1 auto',
                  minWidth: 0,
                  padding: '8px 12px',
                  fontSize: '14px',
                  border: `1px solid ${BORDER}`,
                  borderRadius: '10px',
                  background: PAGE,
                  color: INK_PRIMARY,
                }}
              />
              <button
                type="submit"
                style={{
                  padding: '8px 16px',
                  fontSize: '14px',
                  fontWeight: 500,
                  border: 'none',
                  borderRadius: '10px',
                  background: INK_PRIMARY,
                  color: SURFACE,
                  cursor: 'pointer',
                }}
              >
                Subscribe
              </button>
            </div>
          </form>
        </div>
      </Card>
    </section>
  )
}

// Same footer as smega.eu (icons, copy, links), restyled with inline styles.
const FOOTER_ICONS = {
  email:
    'M4 5h16a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Zm0 2v.217l8 4.8 8-4.8V7H4Zm0 2.783V17h16v-7.217l-7.445 4.47a2 2 0 0 1-2.11 0L4 9.783Z',
  github:
    'M12 2C6.477 2 2 6.486 2 12.021c0 4.418 2.865 8.166 6.839 9.489.5.093.683-.217.683-.483 0-.238-.009-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.455-1.158-1.11-1.466-1.11-1.466-.908-.622.069-.61.069-.61 1.004.071 1.532 1.034 1.532 1.034.892 1.534 2.341 1.09 2.91.834.091-.649.35-1.09.636-1.34-2.221-.254-4.555-1.115-4.555-4.962 0-1.096.39-1.993 1.029-2.695-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.029A9.564 9.564 0 0 1 12 6.844c.85.004 1.705.115 2.504.337 1.909-1.299 2.748-1.029 2.748-1.029.546 1.378.203 2.397.1 2.65.64.702 1.027 1.599 1.027 2.695 0 3.857-2.337 4.705-4.566 4.955.359.31.678.921.678 1.856 0 1.34-.012 2.421-.012 2.75 0 .268.18.58.688.481A9.52 9.52 0 0 0 22 12.021C22 6.486 17.523 2 12 2Z',
  linkedin:
    'M4.983 3.5C4.983 4.604 4.089 5.5 2.983 5.5 1.878 5.5 1 4.604 1 3.5 1 2.395 1.878 1.5 2.983 1.5 4.089 1.5 4.983 2.395 4.983 3.5ZM5 8H1v15h4V8Zm6.5 0H7.5v15h4v-8.062c0-2.246 1.163-3.438 2.993-3.438 1.383 0 2.507.968 2.507 2.938V23H21v-8.938C21 10.072 19.087 8 16.256 8c-1.88 0-3.19.797-3.756 1.723h-.05V8Z',
}

const FOOTER_LINKS = [
  { label: 'Email', href: 'mailto:smega@smega.eu', icon: 'email' },
  { label: 'GitHub', href: 'https://github.com/smega', icon: 'github' },
  { label: 'LinkedIn', href: 'https://www.linkedin.com/in/szabolcs-mega-822b6a63/', icon: 'linkedin' },
]

function Footer() {
  const year = new Date().getFullYear()
  return (
    <footer style={{ borderTop: `1px solid ${BORDER}`, background: 'rgba(255,255,255,0.8)' }}>
      <div
        style={{
          maxWidth: '1100px',
          margin: '0 auto',
          padding: '16px 20px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '16px',
          fontSize: '12px',
          color: INK_MUTED,
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
          <span>
            &copy; {year} <a href="https://smega.eu" style={{ color: 'inherit' }}>Szabolcs Mega</a>.
          </span>
          <span>Built, run and broken (then fixed) by one person.</span>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
          {FOOTER_LINKS.map((l) => (
            <a
              key={l.label}
              href={l.href}
              aria-label={l.label}
              {...(l.href.startsWith('http') ? { target: '_blank', rel: 'noreferrer' } : {})}
              style={{
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                width: '28px',
                height: '28px',
                borderRadius: '50%',
                border: `1px solid ${BORDER}`,
                color: INK_SECONDARY,
              }}
            >
              <svg viewBox="0 0 24 24" aria-hidden="true" style={{ width: '14px', height: '14px' }}>
                <path d={FOOTER_ICONS[l.icon]} fill="currentColor" />
              </svg>
            </a>
          ))}
        </div>
      </div>
    </footer>
  )
}

// Two views behind a hash ("#cluster"), so each has its own shareable URL
// without needing a router or server-side routes.
const TABS = [
  { id: 'services', label: 'Services', hash: '' },
  { id: 'cluster', label: 'Cluster', hash: '#cluster' },
]

function tabFromHash() {
  return window.location.hash === '#cluster' ? 'cluster' : 'services'
}

function useTab() {
  const [tab, setTab] = useState(tabFromHash)
  useEffect(() => {
    const onHash = () => {
      setTab(tabFromHash())
      window.scrollTo(0, 0)
    }
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])
  return tab
}

function SiteHeader({ tab }) {
  return (
    <header style={{ borderBottom: `1px solid ${BORDER}`, background: SURFACE }}>
      <div
        style={{
          maxWidth: '1100px',
          margin: '0 auto',
          padding: '12px 20px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '12px 20px',
          flexWrap: 'wrap',
        }}
      >
        <a href="#" style={{ display: 'flex', alignItems: 'center', gap: '10px', textDecoration: 'none', color: INK_PRIMARY }}>
          <img src="/favicon.svg?v=2" alt="" width="28" height="28" style={{ display: 'block' }} />
          <span style={{ fontWeight: 600, fontSize: '15px' }}>
            Szabolcs Mega <span style={{ color: INK_MUTED, fontWeight: 400 }}>/ status</span>
          </span>
        </a>
        <nav style={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
          {TABS.map((t) => {
            const active = t.id === tab
            return (
              <a
                key={t.id}
                href={t.hash || '#'}
                aria-current={active ? 'page' : undefined}
                style={{
                  padding: '6px 12px',
                  borderRadius: '999px',
                  fontSize: '14px',
                  textDecoration: 'none',
                  background: active ? INK_PRIMARY : 'transparent',
                  color: active ? SURFACE : INK_SECONDARY,
                }}
              >
                {t.label}
              </a>
            )
          })}
          <a href="https://smega.eu" style={{ padding: '6px 12px', fontSize: '14px', color: INK_SECONDARY, textDecoration: 'none' }}>
            smega.eu ↗
          </a>
        </nav>
      </div>
    </header>
  )
}

function PageIntro({ title, children }) {
  return (
    <div style={{ marginBottom: '28px' }}>
      <h1 style={{ fontSize: '28px', fontWeight: 600, margin: '0 0 10px', color: INK_PRIMARY }}>{title}</h1>
      <p style={{ margin: 0, color: INK_SECONDARY, lineHeight: 1.6, maxWidth: '620px' }}>{children}</p>
    </div>
  )
}

function ServicesView({ apiBase }) {
  const [data, setData] = useState(null)
  const [error, setError] = useState('')

  useEffect(() => {
    const load = () => {
      fetch(`${apiBase}/api/status`)
        .then((r) => {
          if (!r.ok) throw new Error(`HTTP ${r.status}`)
          return r.json()
        })
        .then((d) => {
          setData(d)
          setError('')
        })
        .catch((err) => setError(err.message))
    }
    load()
    const interval = setInterval(load, 60_000)
    return () => clearInterval(interval)
  }, [apiBase])

  const summary = useMemo(() => {
    if (!data) return null
    const down = data.services.filter((s) => !s.ok)
    if (down.length === 0) return { color: GOOD, text: 'All systems operational' }
    if (down.length === data.services.length) return { color: CRITICAL, text: 'All systems down' }
    return { color: CRITICAL, text: `${down.length} of ${data.services.length} services ${down.length > 1 ? 'are' : 'is'} having issues` }
  }, [data])

  return (
    <>
      <PageIntro title="Service status">
        The small services I run myself, checked every 5 minutes by a Kubernetes CronJob on my home Raspberry Pi
        cluster. Each strip is the last 24 hours of checks. Every service has a 99.5% availability target (SLO)
        over 30 days, which allows about 3.6 hours of downtime: the error budget shows how much of that is left.
      </PageIntro>

      {error && (
        <div
          style={{
            marginBottom: '20px',
            padding: '12px 14px',
            border: `1px solid ${CRITICAL}33`,
            background: SURFACE,
            borderRadius: '10px',
            color: INK_PRIMARY,
            fontSize: '14px',
          }}
        >
          Failed to load status: {error}
        </div>
      )}

      {!data && !error && <div style={{ color: INK_SECONDARY }}>Loading…</div>}

      {data && (
        <>
          {summary && (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '10px',
                marginBottom: '24px',
                padding: '14px 16px',
                borderRadius: '10px',
                background: SURFACE,
                border: `1px solid ${BORDER}`,
                color: INK_PRIMARY,
                fontWeight: 500,
              }}
            >
              <span style={{ width: '10px', height: '10px', borderRadius: '50%', background: summary.color, display: 'inline-block', flexShrink: 0 }} />
              {summary.text}
            </div>
          )}

          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))',
              gap: '14px',
              marginBottom: '24px',
            }}
          >
            {data.services.map((s) => (
              <ServiceCard key={s.name} s={s} />
            ))}
          </div>

          <BadgesSection apiBase={apiBase} services={data.services} />

          <div style={{ fontSize: '12px', color: INK_MUTED }}>
            Last checked: {data.generatedAt} ·{' '}
            <a href="#cluster" style={{ color: INK_SECONDARY }}>
              What runs these checks
            </a>
          </div>
        </>
      )}
    </>
  )
}

function ClusterView({ apiBase }) {
  return (
    <>
      <h1 style={{ fontSize: '28px', fontWeight: 600, margin: '0 0 10px', color: INK_PRIMARY }}>Raspberry Pi cluster</h1>
      <NodesSection apiBase={apiBase} />
      <HowItWorksSection />
    </>
  )
}

function App() {
  const apiBase = useMemo(() => window.__STATUS_API_BASE__ || 'https://api-status.smega.eu', [])
  const tab = useTab()

  return (
    <>
      <SiteHeader tab={tab} />
      <main style={{ maxWidth: '1100px', margin: '0 auto', padding: '40px 20px 60px' }}>
        {tab === 'cluster' ? <ClusterView apiBase={apiBase} /> : <ServicesView apiBase={apiBase} />}
        <AuthorSection />
      </main>
      <Footer />
    </>
  )
}

render(<App />, document.getElementById('app'))
