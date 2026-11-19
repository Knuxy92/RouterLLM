import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type JSX, type MouseEvent } from "react"
import { adaptLogs, adaptModels, adaptTraffic, fmtDur, fmtInt, type AdaptedLog, type TrafficPoint } from "../lib/data"
import { adminStore } from "../lib/store"
import type { LogEvent, MetricsSummary } from "../lib/types"

type Range = "24h" | "7d"

const BASE = import.meta.env.BASE_URL || "/admin/"

function refreshIcons() {
  const lucide = (window as unknown as { lucide?: { createIcons(): void } }).lucide
  lucide?.createIcons()
}

function openTrace(entry: LogEvent) {
  window.dispatchEvent(new CustomEvent("routerllm:open-trace", { detail: entry }))
}

interface DeltaChipProps {
  cur: number | null | undefined
  before: number | null | undefined
  format: (value: number) => string
  hasPrev: boolean
  weekly: boolean
  invert?: boolean
}

function DeltaChip({ cur, before, format, hasPrev, weekly, invert = false }: DeltaChipProps): JSX.Element | null {
  if (!hasPrev || cur == null || before == null) return null

  const period = weekly ? "7 days" : "24 hours"
  const diff = cur - before
  if (diff === 0)
    return <span className="delta delta-flat has-tip" data-tip={`No change vs previous ${period}`}>±0</span>

  const good = invert ? diff < 0 : diff > 0
  const arrow = diff > 0 ? "↑" : "↓"

  return (
    <span
      className={`delta ${good ? "delta-up" : "delta-down"} has-tip`}
      data-tip={`vs previous ${period}`}
    >
      {arrow}{format(Math.abs(diff))}
    </span>
  )
}

function polyline(values: number[], width: number, height: number, pad: number, min: number, max: number): string {
  const span = max - min || 1
  return values
    .map((value, i) => {
      const x = (i / (values.length - 1)) * width
      const y = height - pad - ((value - min) / span) * (height - pad * 2)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(" ")
}

interface HoverState {
  index: number
  x: number
  tipLeft: number
}

function TrafficChart({ series }: { series: TrafficPoint[] }): JSX.Element {
  const wrapRef = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<HoverState | null>(null)

  const height = 220
  const points = useMemo(() => {
    const values = [series.map((s) => s.req), series.map((s) => s.err)]
    const all = values.flat()
    const min = Math.min(...all)
    const max = Math.max(...all)
    const grid = [0.25, 0.5, 0.75].map((t) => (height * t).toFixed(1))
    return { values, min, max, grid }
  }, [series])

  if (!series.some((s) => s.req > 0))
    return <p className="flex h-56 items-center justify-center text-xs text-muted-foreground">No traffic recorded yet.</p>

  const onEnter = (index: number) => (event: MouseEvent<HTMLDivElement>) => {
    const col = event.currentTarget
    const x = col.offsetLeft + col.offsetWidth / 2
    const width = wrapRef.current?.clientWidth ?? 0
    setHover({ index, x, tipLeft: Math.min(Math.max(x, 80), width - 80) })
  }

  const hovered = hover ? series[hover.index] : null

  return (
    <div ref={wrapRef} className="chart-wrap">
      <svg viewBox={`0 0 640 ${height}`} className="w-full" style={{ height }} preserveAspectRatio="none">
        {points.grid.map((y) => (
          <line key={y} x1="0" x2="640" y1={y} y2={y} stroke="hsl(var(--border))" strokeWidth="1" />
        ))}
        <polyline
          points={polyline(points.values[0], 640, height, 12, points.min, points.max)}
          fill="none"
          stroke="hsl(var(--primary))"
          strokeWidth="2"
        />
        <polyline
          points={polyline(points.values[1], 640, height, 12, points.min, points.max)}
          fill="none"
          stroke="hsl(var(--destructive))"
          strokeWidth="2"
          strokeDasharray="4 3"
        />
      </svg>
      <div className={`chart-guide ${hover ? "show" : ""}`} style={hover ? { left: `${hover.x}px` } : undefined}></div>
      <div className="chart-cols" onMouseLeave={() => setHover(null)}>
        {series.map((point, i) => (
          <div key={point.label + i} className="chart-col" onMouseEnter={onEnter(i)}></div>
        ))}
      </div>
      <div className={`chart-tip ${hover ? "show" : ""}`} style={hover ? { left: `${hover.tipLeft}px` } : undefined}>
        {hovered && <><b>{hovered.label}</b><br />{hovered.tip}</>}
      </div>
    </div>
  )
}

function FailureRow({ row }: { row: AdaptedLog }): JSX.Element {
  return (
    <li
      className={`flex cursor-pointer items-center gap-3 px-5 py-3 hover:bg-muted/50 ${row.level === "error" ? "log-error-row" : ""}`}
      data-open-trace
      data-seq={row.seq}
      onClick={() => openTrace(row.entry)}
    >
      <span className="font-mono text-xs text-muted-foreground">{row.time}</span>
      <span className={`badge ${row.level === "error" ? "tone-error" : "tone-warn"}`}>{row.level}</span>
      <span className="min-w-0 flex-1 truncate">{row.msg}</span>
      <span className="hidden font-mono text-xs text-muted-foreground sm:inline">{row.status ?? "—"}</span>
      <i data-lucide="chevron-right" className="size-4 text-muted-foreground"></i>
    </li>
  )
}

export default function Dashboard(): JSX.Element {
  const state = useSyncExternalStore(adminStore.subscribe, adminStore.getSnapshot, adminStore.getSnapshot)
  const { status, metrics } = state
  const [range, setRange] = useState<Range>("24h")
  const [reloading, setReloading] = useState(false)
  const weekly = range === "7d"

  useEffect(() => {
    refreshIcons()
  })

  const current: MetricsSummary | undefined = weekly ? metrics?.global_weekly : metrics?.global
  const previous: MetricsSummary | undefined = weekly ? metrics?.global_weekly_prev : metrics?.global_prev
  const hasPrev = !!(previous && previous.req > 0)
  const errPct = current && current.req > 0 ? ((current.err / current.req) * 100).toFixed(2) : null

  const series = useMemo(() => adaptTraffic(metrics, range), [metrics, range])

  const models = useMemo(() => adaptModels(status, metrics), [status, metrics])
  const ttftModels = [...models].sort((a, b) => b.reqNum - a.reqNum).slice(0, 10)
  const maxTtft = Math.max(...ttftModels.map((m) => m.ttft), 1)

  const failures = useMemo(() => adaptLogs(state.recentFailures), [state.recentFailures])

  const modelEntries = Object.entries(metrics?.models || {})
  const modelTotal = modelEntries.reduce((sum, [, m]) => sum + m.req, 0)
  const topModels = [...modelEntries].sort((a, b) => b[1].req - a[1].req).slice(0, 5)

  async function handleReload() {
    setReloading(true)
    try {
      await adminStore.reloadConfig()
    } finally {
      setReloading(false)
    }
  }

  return (
    <section id="page-dashboard" className="page">
      <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
          <p className="text-sm text-muted-foreground">
            Live overview of the router | last {weekly ? "7 days" : "24 hours"}.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <div className="seg">
            <button type="button" className={`seg-btn ${range === "24h" ? "active" : ""}`} onClick={() => setRange("24h")}>24h</button>
            <button type="button" className={`seg-btn ${range === "7d" ? "active" : ""}`} onClick={() => setRange("7d")}>7d</button>
          </div>
          <button
            type="button"
            disabled={reloading}
            onClick={handleReload}
            className="inline-flex items-center gap-2 rounded-md border bg-background px-3 py-1.5 text-sm shadow-sm hover:bg-accent disabled:opacity-50"
          >
            <i data-lucide="refresh-cw" className="size-3.5"></i>Reload config
          </button>
        </div>
      </div>

      <div className="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-5">
        <div className="rounded-lg border bg-card p-4 shadow-sm">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">Requests {weekly ? "7d" : "24h"}</p>
            <i data-lucide="zap" className="size-3.5 text-muted-foreground"></i>
          </div>
          <p className="mt-1.5 font-mono text-xl font-semibold">
            {fmtInt(current?.req ?? 0)}
            <DeltaChip cur={current?.req ?? 0} before={previous?.req} format={fmtInt} hasPrev={hasPrev} weekly={weekly} />
          </p>
          <span className="badge tone-info mt-1.5">{status?.models_serving ?? 0} models serving</span>
        </div>

        <div className="rounded-lg border bg-card p-4 shadow-sm">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">
              <span className="has-tip" data-tip="Share of requests that succeeded (non-error responses)">Success rate</span>
            </p>
            <i data-lucide="gauge" className="size-3.5 text-muted-foreground"></i>
          </div>
          <p className="mt-1.5 font-mono text-xl font-semibold">
            {current && current.req > 0 ? (
              <>
                {current.success_pct}<span className="text-sm">%</span>
                <DeltaChip cur={current.success_pct} before={previous?.success_pct} format={(v) => v.toFixed(1) + "%"} hasPrev={hasPrev} weekly={weekly} />
              </>
            ) : "—"}
          </p>
          <span className="badge tone-ok mt-1.5">
            {current && current.req > 0 ? `${fmtInt(current.req - current.err)} ok` : "no traffic yet"}
          </span>
        </div>

        <div className="rounded-lg border bg-card p-4 shadow-sm">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">
              <span className="has-tip" data-tip="Median time to first token · half of requests were faster">Median TTFT</span>
            </p>
            <i data-lucide="clock" className="size-3.5 text-muted-foreground"></i>
          </div>
          <p className="mt-1.5 font-mono text-xl font-semibold">
            {current?.ttft_p50_ms ? (
              <>
                {fmtDur(current.ttft_p50_ms)}
                <DeltaChip cur={current.ttft_p50_ms} before={previous?.ttft_p50_ms} format={fmtDur} hasPrev={hasPrev} weekly={weekly} invert />
              </>
            ) : "—"}
          </p>
          <span className="badge tone-info mt-1.5">{current?.ttft_p95_ms ? `p95 ${fmtDur(current.ttft_p95_ms)}` : ""}</span>
        </div>

        <div className="rounded-lg border bg-card p-4 shadow-sm">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">
              <span className="has-tip" data-tip="Output tokens streamed per second across all models">Throughput</span>
            </p>
            <i data-lucide="trending-up" className="size-3.5 text-muted-foreground"></i>
          </div>
          <p className="mt-1.5 font-mono text-xl font-semibold">
            {current?.tok_per_sec ? (
              <>
                {fmtInt(Math.round(current.tok_per_sec))}<span className="text-sm"> tok/s</span>
                <DeltaChip cur={current.tok_per_sec} before={previous?.tok_per_sec} format={(v) => fmtInt(Math.round(v))} hasPrev={hasPrev} weekly={weekly} />
              </>
            ) : "—"}
          </p>
          <span className="badge tone-info mt-1.5">{current?.tokens ? `${fmtInt(current.tokens)} tokens out` : ""}</span>
        </div>

        <div className="col-span-2 rounded-lg border bg-card p-4 shadow-sm lg:col-span-1">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">Errors {weekly ? "7d" : "24h"}</p>
            <i data-lucide="shield-alert" className="size-3.5 text-muted-foreground"></i>
          </div>
          <p className="mt-1.5 font-mono text-xl font-semibold">
            {fmtInt(current?.err ?? 0)}
            <DeltaChip cur={current?.err ?? 0} before={previous?.err} format={fmtInt} hasPrev={hasPrev} weekly={weekly} invert />
          </p>
          <span className="badge tone-error mt-1.5">{errPct != null ? `${errPct}% of requests` : ""}</span>
        </div>
      </div>

      <div className="mb-5 grid gap-3 lg:grid-cols-3">
        <div className="min-w-0 rounded-lg border bg-card p-5 shadow-sm lg:col-span-2">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-y-1">
            <div>
              <p className="text-sm font-medium">Traffic &amp; failures</p>
              <p className="text-xs text-muted-foreground">
                {weekly
                  ? "Requests vs upstream errors · daily buckets · last 7 days"
                  : "Requests vs upstream errors · hourly buckets · last 24h"}
              </p>
            </div>
            <div className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="flex items-center gap-1.5"><span className="h-0.5 w-3 rounded bg-primary"></span>requests</span>
              <span className="flex items-center gap-1.5"><span className="h-0.5 w-3 rounded bg-destructive"></span>errors</span>
            </div>
          </div>
          <TrafficChart series={series} />
        </div>

        <div className="min-w-0 rounded-lg border bg-card p-5 shadow-sm">
          <p className="text-sm font-medium">TTFT p50 by model</p>
          <p className="mb-3 text-xs text-muted-foreground">
            Time to first token · p50 · 24h · top {ttftModels.length} of {models.length} models, by requests
          </p>
          <div className="flex max-h-64 flex-col gap-3.5 overflow-y-auto pr-1 text-xs" id="ttft-list">
            {ttftModels.length === 0 ? (
              <p className="text-xs text-muted-foreground">No traffic recorded yet.</p>
            ) : ttftModels.map((m) => (
              <div key={m.name}>
                <div className="mb-1 flex justify-between">
                  <span className="font-mono">{m.name}</span>
                  <span className="font-mono text-muted-foreground">{fmtDur(m.ttft)}</span>
                </div>
                <div className="h-2 rounded-full bg-muted">
                  <div className="h-2 rounded-full bg-primary" style={{ width: `${Math.round((m.ttft / maxTtft) * 100)}%` }}></div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      <div className="grid gap-3 lg:grid-cols-3">
        <div className="min-w-0 overflow-hidden rounded-lg border bg-card shadow-sm lg:col-span-2">
          <div className="flex items-center justify-between border-b px-5 py-3.5">
            <div>
              <p className="text-sm font-medium">Recent failures</p>
              <p className="text-xs text-muted-foreground">Click a row to open the full request trace</p>
            </div>
            <a href={BASE + "logs"} className="text-xs text-muted-foreground underline-offset-2 hover:underline">View all logs</a>
          </div>
          <ul className="divide-y text-sm" id="recent-failures">
            {failures.length === 0 ? (
              <li className="px-5 py-6 text-center text-xs text-muted-foreground">No failures in the retained window.</li>
            ) : failures.map((row) => <FailureRow key={row.seq} row={row} />)}
          </ul>
        </div>

        <div className="rounded-lg border bg-card p-5 shadow-sm">
          <p className="text-sm font-medium">Top models · 24h</p>
          <p className="mb-4 text-xs text-muted-foreground">Share of requests</p>
          <div className="flex flex-col gap-4 text-xs">
            {topModels.length === 0 || modelTotal === 0 ? (
              <p className="text-xs text-muted-foreground">No traffic recorded yet.</p>
            ) : topModels.map(([name, m]) => {
              const pct = Math.max(1, Math.round((m.req / modelTotal) * 100))
              return (
                <div key={name}>
                  <div className="mb-1 flex items-center justify-between">
                    <span className="font-mono">{name}</span>
                    <span className="text-muted-foreground">{fmtInt(m.req)} · {pct}%</span>
                  </div>
                  <div className="h-2 rounded-full bg-muted">
                    <div className="h-2 rounded-full bg-primary/70" style={{ width: `${pct}%` }}></div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      </div>
    </section>
  )
}
