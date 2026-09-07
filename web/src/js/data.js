// Adapter layer: transforms backend payloads (/status, /metrics, /requests)
// into the shapes render.js draws. No mock data lives here — every value is
// derived from the last API response; missing data renders as empty/zero.

export const STATUS_META = {
  healthy: { dot: "dot-live", badge: "tone-ok", label: "Healthy" },
  degraded: { dot: "dot-warn", badge: "tone-warn", label: "Degraded" },
  disabled: { dot: "dot-muted", badge: "tone-info", label: "Disabled" },
}

export const NOTE_TONE = { ok: "tone-ok", warn: "tone-warn", error: "tone-error", info: "tone-info" }

export const KEY_TONE = {
  healthy: "tone-ok",
  "rate-limited": "tone-warn",
  cooldown: "tone-warn",
  invalid: "tone-error",
  disabled: "tone-info",
}

export function hashStr(s) {
  let h = 0
  for (const c of s) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return h
}

export function fmtInt(n) {
  return (n ?? 0).toLocaleString("en-US")
}

export function fmtCompact(n) {
  if (n == null) return "—"
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace(/\.0$/, "") + "M"
  if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, "") + "k"
  return String(n)
}

export function esc(s) {
  return String(s ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c])
}

// ----- providers ---------------------------------------------------------------

function keyStatus(k) {
  if (k.disabled) return "disabled"
  if (k.alive) return "healthy"
  return (k.cooldown_left_seconds ?? 0) > 0 ? "cooldown" : "invalid"
}

function providerStatus(p) {
  if (p.disabled) return "disabled"
  if (p.keys_alive === 0) return "degraded"
  const req = p.requests || 0
  if (req > 0 && (p.errors || 0) / req > 0.05) return "degraded"
  return "healthy"
}

export function adaptProviders(status, metrics) {
  if (!status) return []

  return (status.providers || []).map((p) => {
    const sum = metrics?.providers?.[p.name]
    const keyList = (p.keys || []).map((k, i) => ({
      id: `${p.name[0]}#${i + 1}`,
      masked: k.masked,
      status: keyStatus(k),
      cooldown: k.cooldown_left_seconds || 0,
      err: null,
      on: !k.disabled,
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

// Provider-weighted TTFT p50 (ms) across its legs, from metrics.legs.
// Replaces the old static PROVIDER_AGG map.
export function providerAgg(metrics) {
  const out = {}
  for (const [route, s] of Object.entries(metrics?.legs || {})) {
    const name = route.split("/")[0]
    const acc = out[name] || (out[name] = { req: 0, weighted: 0 })
    acc.req += s.req
    acc.weighted += s.req * (s.ttft_p50_ms || 0)
  }
  for (const [name, acc] of Object.entries(out)) {
    out[name] = acc.req > 0 ? Math.round(acc.weighted / acc.req) : null
  }
  return out
}

// ----- models & fallback chains --------------------------------------------------

function legToneNote(leg) {
  if (leg.disabled) return { tone: "info", note: leg.note || "disabled" }
  if (leg.provider_disabled) return { tone: "error", note: leg.note || "provider disabled" }
  if (leg.note) {
    const tone = /error|401|403/i.test(leg.note) ? "error" : /503|429|5\d\d|rate|spike/i.test(leg.note) ? "warn" : "info"
    return { tone, note: leg.note }
  }
  if (leg.active) return { tone: "ok", note: "primary" }
  return { tone: "info", note: "standby" }
}

export function adaptModels(status, metrics) {
  if (!status) return []

  return (status.models || []).map((m) => {
    const sum = metrics?.models?.[m.model_id]
    return {
      name: m.model_id,
      req: fmtCompact(sum?.req ?? 0),
      reqNum: sum?.req ?? 0,
      ok: sum && sum.req > 0 ? sum.success_pct + "%" : "—",
      ttft: sum?.ttft_p50_ms || 0,
      on: !m.disabled,
      serving: m.serving,
      legs: (m.chain || []).map((leg) => ({
        route: `${leg.provider}/${leg.model}`,
        provider: leg.provider,
        model: leg.model,
        active: !!leg.active,
        ...legToneNote(leg),
        on: !leg.disabled,
      })),
    }
  })
}

// ----- request logs ---------------------------------------------------------------

function logLevel(e) {
  if (e.status === 0) return "error"
  if (e.status < 400) return "info"
  if (e.status === 429 || (e.status >= 500 && e.status < 600) || (e.attempts?.length ?? 0) > 1) return "warn"
  return "error"
}

function logTime(iso, ageH) {
  const d = new Date(iso)
  const pad = (n) => String(n).padStart(2, "0")
  if (ageH < 24) return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  const mon = d.toLocaleDateString("en-US", { month: "short" })
  return `${mon} ${d.getDate()} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function logMsg(e) {
  const via = `${e.model} → ${e.provider || "—"}`
  if (e.err) return `${via}: ${e.err}`
  if (e.status >= 400) return `${via} failed: status=${e.status}${e.attempts?.length > 1 ? ` (${e.attempts.length} attempts)` : ""}`
  return `routed ${via} (key ${e.key || "—"}) · ${e.status} · first token ${e.ttft_ms} ms`
}

export function adaptLogs(entries) {
  const now = Date.now()
  return [...(entries || [])]
    .sort((a, b) => b.seq - a.seq)
    .map((e) => {
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
        ttft: e.ttft_ms ? `${fmtInt(e.ttft_ms)} ms` : "—",
        tps,
        status: e.status || null,
        ageH,
        attempts: e.attempts || [],
        entry: e,
      }
    })
}

// ----- charts ----------------------------------------------------------------------

// One chart point from a metrics Window. `range` picks the window series:
// "24h" → hourly (2h buckets), "7d" → weekly (24h buckets).
export function adaptTraffic(metrics, range = "24h") {
  const windows = range === "7d" ? metrics?.weekly : metrics?.hourly
  return (windows || []).map((w) => {
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

// 7-day rollups for the drawer/global widgets: labels, per-day success %,
// and [p50, p95] TTFT pairs (the backend tracks no p99).
export function adaptWeekly(metrics) {
  const WEEK = []
  const SUCCESS = []
  const TTFT_7D = []
  for (const w of metrics?.weekly || []) {
    const d = new Date(w.start * 1000)
    WEEK.push(d.toLocaleDateString("en-US", { month: "short", day: "numeric" }))
    SUCCESS.push(w.req > 0 ? (((w.req - w.err) / w.req) * 100).toFixed(1) + "%" : "—")
    TTFT_7D.push([w.ttft_p50_ms || 0, w.ttft_p95_ms || 0])
  }
  return { WEEK, SUCCESS, TTFT_7D }
}

// ----- provider drawer table ----------------------------------------------------------

export function providerModelRows(provider, metrics, status) {
  const rows = []
  const seen = new Set()

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
