import type {
  KeyState,
  LogEvent,
  MetricsPayload,
  ModelStatus,
  ProviderStatus,
  StatusPayload,
} from "./types"

export const STATUS_META: Record<string, { dot: string; badge: string; label: string }> = {
  healthy: { dot: "dot-live", badge: "tone-ok", label: "Healthy" },
  degraded: { dot: "dot-warn", badge: "tone-warn", label: "Degraded" },
  disabled: { dot: "dot-muted", badge: "tone-info", label: "Disabled" },
}

export const NOTE_TONE: Record<string, string> = {
  ok: "tone-ok",
  warn: "tone-warn",
  error: "tone-error",
  info: "tone-info",
}

export const KEY_TONE: Record<string, string> = {
  healthy: "tone-ok",
  cooldown: "tone-warn",
  invalid: "tone-error",
  disabled: "tone-info",
}

export function fmtDur(ms: number | null | undefined): string {
  if (ms == null || ms <= 0) return "—"
  if (ms < 1000) return `${Math.round(ms)} ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(ms < 10000 ? 2 : 1)} s`
  return `${Math.floor(ms / 60000)}m ${Math.round((ms % 60000) / 1000)}s`
}

export function fmtInt(n: number | null | undefined): string {
  return (n ?? 0).toLocaleString("en-US")
}

export function fmtCompact(n: number | null | undefined): string {
  if (n == null) return "—"
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M"
  if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, "") + "k"
  return String(n)
}

const ESCAPES: Record<string, string> = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
}

export function esc(s: unknown): string {
  return String(s ?? "").replace(/[&<>"]/g, (c) => ESCAPES[c])
}

export interface AdaptedKey {
  id: string
  masked: string
  status: string
  cooldown: number
  err: null
  on: boolean
  quota: KeyState["quota"]
}

export interface AdaptedProvider {
  name: string
  style: string
  base_url: string
  status: string
  keys: string
  req: string
  reqNum: number
  err: string
  up: string
  on: boolean
  serving: boolean
  model_count: number
  share: string | null
  keyList: AdaptedKey[]
}

function keyStatus(k: KeyState): string {
  if (k.disabled) return "disabled"
  if (k.alive) return "healthy"
  return (k.cooldown_left_seconds ?? 0) > 0 ? "cooldown" : "invalid"
}

function providerStatus(p: ProviderStatus): string {
  if (p.disabled) return "disabled"
  if (p.keys_alive === 0) return "degraded"
  const req = p.requests || 0
  if (req > 0 && (p.errors || 0) / req > 0.05) return "degraded"
  return "healthy"
}

export function adaptProviders(status: StatusPayload | null, metrics: MetricsPayload | null): AdaptedProvider[] {
  if (!status) return []

  return (status.providers || []).map((p): AdaptedProvider => {
    const sum = metrics?.providers?.[p.name]
    const keyList = (p.keys || []).map((k, i): AdaptedKey => ({
      id: `${p.name[0]}#${i + 1}`,
      masked: k.masked,
      status: keyStatus(k),
      cooldown: k.cooldown_left_seconds || 0,
      err: null,
      on: !k.disabled,
      quota: k.quota || null,
    }))

    const req = sum ? sum.req : p.requests || 0
    const err = sum ? sum.err : p.errors || 0
    const pct = req > 0 ? (err / req) * 100 : 0
    const errHtml = err > 0 && pct >= 1
      ? `<span class="text-destructive">${fmtInt(err)} (${pct.toFixed(1)}%)</span>`
      : `${fmtInt(err)} (${pct.toFixed(1)}%)`

    return {
      name: p.name,
      style: p.style,
      base_url: p.base_url,
      status: providerStatus(p),
      keys: `${p.keys_alive}/${p.keys_total} alive`,
      req: fmtInt(req),
      reqNum: req,
      err: errHtml,
      up: sum && sum.req > 0 ? sum.uptime_pct + "%" : "—",
      on: !p.disabled,
      serving: p.serving,
      model_count: p.model_count,
      share: p.share || null,
      keyList,
    }
  })
}

/** Provider-weighted TTFT p50 (ms) across its legs, from metrics.legs. */
export function providerAgg(metrics: MetricsPayload | null): Record<string, number | null> {
  const out: Record<string, { req: number; weighted: number }> = {}
  for (const [route, s] of Object.entries(metrics?.legs || {})) {
    const name = route.split("/")[0]
    const acc = out[name] || (out[name] = { req: 0, weighted: 0 })
    acc.req += s.req
    acc.weighted += s.req * (s.ttft_p50_ms || 0)
  }
  const result: Record<string, number | null> = {}
  for (const [name, acc] of Object.entries(out)) {
    result[name] = acc.req > 0 ? Math.round(acc.weighted / acc.req) : null
  }
  return result
}

interface ToneNote {
  tone: string
  note: string
}

function legToneNote(leg: ModelStatus["chain"][number]): ToneNote {
  if (leg.disabled) return { tone: "info", note: leg.note || "disabled" }
  if (leg.provider_disabled) return { tone: "error", note: leg.note || "provider disabled" }
  if (leg.note) {
    const tone = /error|401|403/i.test(leg.note) ? "error" : /503|429|5\d\d|rate|spike/i.test(leg.note) ? "warn" : "info"
    return { tone, note: leg.note }
  }
  if (leg.active) return { tone: "ok", note: "primary" }
  return { tone: "info", note: "standby" }
}

export interface AdaptedLeg extends ToneNote {
  route: string
  provider: string
  model: string
  stylecall: string | null
  active: boolean
  on: boolean
}

export interface AdaptedModel {
  name: string
  req: string
  reqNum: number
  ok: string
  ttft: number
  on: boolean
  serving: boolean
  legs: AdaptedLeg[]
}

export function adaptModels(status: StatusPayload | null, metrics: MetricsPayload | null): AdaptedModel[] {
  if (!status) return []

  return (status.models || []).map((m): AdaptedModel => {
    const sum = metrics?.models?.[m.model_id]
    return {
      name: m.model_id,
      req: fmtCompact(sum?.req ?? 0),
      reqNum: sum?.req ?? 0,
      ok: sum && sum.req > 0 ? sum.success_pct + "%" : "—",
      ttft: sum?.ttft_p50_ms || 0,
      on: !m.disabled,
      serving: m.serving,
      legs: (m.chain || []).map((leg): AdaptedLeg => ({
        route: `${leg.provider}/${leg.model}`,
        provider: leg.provider,
        model: leg.model,
        stylecall: leg.stylecall || null,
        active: !!leg.active,
        ...legToneNote(leg),
        on: !leg.disabled,
      })),
    }
  })
}

function logLevel(e: LogEvent): string {
  if (e.status === 0) return "error"
  if (e.status < 400) return "info"
  if (e.status === 429 || (e.status >= 500 && e.status < 600) || (e.attempts?.length ?? 0) > 1) return "warn"
  return "error"
}

function logTime(iso: string, ageH: number): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, "0")
  if (ageH < 24) return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  const mon = d.toLocaleDateString("en-US", { month: "short" })
  return `${mon} ${d.getDate()} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function logMsg(e: LogEvent): string {
  const via = `${e.model} → ${e.provider || "—"}`
  if (e.err) return `${via}: ${e.err}`
  if (e.status >= 400) return `${via} failed: status=${e.status}${e.attempts && e.attempts.length > 1 ? ` (${e.attempts.length} attempts)` : ""}`
  return `routed ${via} (key ${e.key || "—"}) · ${e.status} · first token ${fmtDur(e.ttft_ms)}`
}

export interface AdaptedLog {
  seq: number
  time: string
  level: string
  msg: string
  provider: string
  model: string
  key: string
  ttft: string
  tps: string | null
  status: number | null
  ageH: number
  attempts: NonNullable<LogEvent["attempts"]>
  entry: LogEvent
}

export function adaptLogs(entries: LogEvent[] | null | undefined): AdaptedLog[] {
  const now = Date.now()
  return [...(entries || [])]
    .sort((a, b) => b.seq - a.seq)
    .map((e): AdaptedLog => {
      const ageH = Math.max(0, (now - new Date(e.time).getTime()) / 3600e3)
      const tps = e.duration_ms > 0 && e.tokens_out > 0
        ? (e.tokens_out / (e.duration_ms / 1000)).toFixed(1)
        : null
      return {
        seq: e.seq,
        time: logTime(e.time, ageH),
        level: logLevel(e),
        msg: logMsg(e),
        provider: e.provider || "—",
        model: e.model || "—",
        key: e.key || "—",
        ttft: e.ttft_ms ? fmtDur(e.ttft_ms) : "—",
        tps,
        status: e.status || null,
        ageH,
        attempts: e.attempts || [],
        entry: e,
      }
    })
}

export interface TrafficPoint {
  label: string
  req: number
  err: number
  p50: number
  p95: number
  tip: string
}

/** One chart point from a metrics Window. range: "24h" hourly, "7d" weekly. */
export function adaptTraffic(metrics: MetricsPayload | null, range: "24h" | "7d" = "24h"): TrafficPoint[] {
  const windows = range === "7d" ? metrics?.weekly : metrics?.hourly
  return (windows || []).map((w): TrafficPoint => {
    const d = new Date(w.start * 1000)
    const label = range === "7d"
      ? d.toLocaleDateString("en-US", { month: "short", day: "numeric" })
      : `${String(d.getHours()).padStart(2, "0")}:00`
    const pct = w.req > 0 ? ((w.err / w.req) * 100).toFixed(1) : "0.0"
    return {
      label,
      req: w.req,
      err: w.err,
      p50: w.ttft_p50_ms || 0,
      p95: w.ttft_p95_ms || 0,
      tip: `${fmtInt(w.req)} req · ${fmtInt(w.err)} err (${pct}%)`,
    }
  })
}

export interface ProviderModelRow {
  model: string
  modelId: string
  route: string
  tone: string
  req: number
  p50: number
  p95: number
  tps: number
}

export function providerModelRows(
  provider: string,
  metrics: MetricsPayload | null,
  status: StatusPayload | null,
): ProviderModelRow[] {
  const rows: ProviderModelRow[] = []
  const seen = new Set<string>()

  for (const m of status?.models || []) {
    for (const leg of m.chain || []) {
      if (leg.provider !== provider) continue
      const route = `${leg.provider}/${leg.model}`
      if (seen.has(route)) continue
      seen.add(route)
      const s = metrics?.legs?.[route]
      rows.push({
        model: m.model_id,
        modelId: leg.model,
        route,
        tone: legToneNote(leg).tone,
        req: s?.req || 0,
        p50: s?.ttft_p50_ms || 0,
        p95: s?.ttft_p95_ms || 0,
        tps: s?.tok_per_sec || 0,
      })
    }
  }

  if (rows.length === 0) {
    for (const [route, s] of Object.entries(metrics?.legs || {})) {
      if (!route.startsWith(provider + "/")) continue
      rows.push({
        model: route.slice(provider.length + 1),
        modelId: route.slice(provider.length + 1),
        route,
        tone: "info",
        req: s.req || 0,
        p50: s.ttft_p50_ms || 0,
        p95: s.ttft_p95_ms || 0,
        tps: s.tok_per_sec || 0,
      })
    }
  }

  return rows.sort((a, b) => b.req - a.req)
}
