// Static mock dataset — identical numbers to the approved wireframe.
// Swap this module for api.js calls when the Go backend exposes metrics.

export const PROVIDERS = [
  { name: "google", status: "degraded", keys: "3 · <span class=\"text-amber-600\">1 cooldown</span>", req: "486,210", err: "<span class=\"text-destructive\">9,204 (1.9%)</span>", up: "99.1%", on: true },
  { name: "openai", status: "healthy", keys: "2 · all healthy", req: "512,877", err: "3,117 (0.6%)", up: "99.9%", on: true },
  { name: "anthropic", status: "healthy", keys: "1 · healthy", req: "241,356", err: "412 (0.2%)", up: "99.95%", on: true },
  { name: "openrouter", status: "healthy", keys: "1 · healthy", req: "132,480", err: "988 (0.7%)", up: "99.8%", on: true },
  { name: "deepseek", status: "healthy", keys: "1 · healthy", req: "126,619", err: "1,013 (0.8%)", up: "99.6%", on: true },
  { name: "groq", status: "degraded", keys: "2 · <span class=\"text-amber-600\">1 rate-limited</span>", req: "121,455", err: "<span class=\"text-destructive\">2,431 (2.0%)</span>", up: "98.4%", on: true },
  { name: "azure-openai", status: "healthy", keys: "1 · healthy", req: "64,900", err: "152 (0.2%)", up: "99.99%", on: true },
  { name: "together", status: "healthy", keys: "2 · all healthy", req: "83,215", err: "749 (0.9%)", up: "99.5%", on: true },
  { name: "xai", status: "healthy", keys: "1 · healthy", req: "74,930", err: "1,349 (1.8%)", up: "99.2%", on: true },
  { name: "fireworks", status: "healthy", keys: "1 · healthy", req: "41,220", err: "388 (0.9%)", up: "99.4%", on: true },
  { name: "bedrock", status: "healthy", keys: "1 · healthy", req: "27,510", err: "96 (0.3%)", up: "100%", on: true },
  { name: "mistral", status: "healthy", keys: "1 · healthy", req: "24,552", err: "221 (0.9%)", up: "99.3%", on: true },
  { name: "cerebras", status: "degraded", keys: "1 · <span class=\"text-amber-600\">rate-limited</span>", req: "18,904", err: "<span class=\"text-destructive\">842 (4.5%)</span>", up: "97.8%", on: true },
  { name: "cohere", status: "healthy", keys: "1 · healthy", req: "6,410", err: "52 (0.8%)", up: "99.7%", on: true },
  { name: "volcengine", status: "disabled", keys: "<span class=\"text-destructive\">1 · invalid (401)</span>", req: "29,807", err: "<span class=\"text-destructive\">2,742 (9.2%)</span>", up: "—", on: false },
]

export const STATUS_META = {
  healthy: { dot: "dot-live", badge: "tone-ok", label: "Healthy" },
  degraded: { dot: "dot-warn", badge: "tone-warn", label: "Degraded" },
  disabled: { dot: "dot-muted", badge: "tone-info", label: "Disabled" },
}

export const NOTE_TONE = { ok: "tone-ok", warn: "tone-warn", error: "tone-error", info: "tone-info" }

export const MODELS = [
  { name: "gemini-3.8-flash", req: "486k", ok: "99.1%", ttft: 388, on: true, legs: [
    { route: "google/gemini-3.8-flash", tone: "warn", note: "503 spike" },
    { route: "openrouter/gemini-3.8-flash", tone: "info", note: "standby" },
  ]},
  { name: "gpt-5.2-mini", req: "513k", ok: "98.8%", ttft: 512, on: true, legs: [
    { route: "openai/gpt-5.2-mini", tone: "ok", note: "healthy" },
    { route: "azure-openai/gpt-5.2-mini", tone: "info", note: "standby" },
  ]},
  { name: "claude-sonnet-4.8", req: "241k", ok: "99.6%", ttft: 843, on: true, legs: [
    { route: "anthropic/claude-sonnet-4.8", tone: "ok", note: "healthy" },
    { route: "openrouter/claude-sonnet-4.8", tone: "info", note: "standby" },
  ]},
  { name: "gemini-3.8-pro", req: "96k", ok: "99.4%", ttft: 612, on: true, legs: [
    { route: "google/gemini-3.8-pro", tone: "warn", note: "503 spike" },
    { route: "openrouter/gemini-3.8-pro", tone: "info", note: "standby" },
  ]},
  { name: "deepseek-v4", req: "88k", ok: "99.2%", ttft: 421, on: true, legs: [
    { route: "deepseek/deepseek-v4", tone: "ok", note: "healthy" },
  ]},
  { name: "grok-4-fast", req: "75k", ok: "98.2%", ttft: 356, on: true, legs: [
    { route: "xai/grok-4-fast", tone: "ok", note: "healthy" },
    { route: "openrouter/grok-4-fast", tone: "info", note: "standby" },
  ]},
  { name: "claude-haiku-4.5", req: "64k", ok: "99.7%", ttft: 524, on: true, legs: [
    { route: "anthropic/claude-haiku-4.5", tone: "ok", note: "healthy" },
    { route: "together/claude-haiku-4.5", tone: "info", note: "standby" },
  ]},
  { name: "gpt-5.2", req: "59k", ok: "99.0%", ttft: 738, on: true, legs: [
    { route: "azure-openai/gpt-5.2", tone: "ok", note: "healthy" },
    { route: "openai/gpt-5.2", tone: "info", note: "standby" },
  ]},
  { name: "llama-4-maverick", req: "44k", ok: "92.4%", ttft: 297, on: true, legs: [
    { route: "groq/llama-4-maverick", tone: "error", note: "401 · key invalid" },
    { route: "together/llama-4-maverick", tone: "info", note: "standby" },
  ]},
  { name: "deepseek-r2", req: "38k", ok: "98.9%", ttft: 468, on: true, legs: [
    { route: "deepseek/deepseek-r2", tone: "ok", note: "healthy" },
    { route: "fireworks/deepseek-r2", tone: "info", note: "standby" },
  ]},
  { name: "qwen3-max", req: "30k", ok: "97.8%", ttft: 549, on: true, legs: [
    { route: "volcengine/qwen3-max", tone: "error", note: "401 · key invalid" },
    { route: "cerebras/qwen3-max", tone: "warn", note: "rate-limited" },
  ]},
  { name: "mistral-large-3", req: "25k", ok: "99.1%", ttft: 445, on: true, legs: [
    { route: "mistral/mistral-large-3", tone: "ok", note: "healthy" },
  ]},
  { name: "llama-4-scout", req: "18k", ok: "98.5%", ttft: 268, on: true, legs: [
    { route: "together/llama-4-scout", tone: "ok", note: "healthy" },
    { route: "fireworks/llama-4-scout", tone: "info", note: "standby" },
  ]},
  { name: "claude-opus-4.6", req: "12k", ok: "99.8%", ttft: 1180, on: true, legs: [
    { route: "anthropic/claude-opus-4.6", tone: "ok", note: "healthy" },
    { route: "bedrock/claude-opus-4.6", tone: "info", note: "standby" },
  ]},
  { name: "gpt-5.2-codex", req: "8.9k", ok: "99.3%", ttft: 902, on: true, legs: [
    { route: "openai/gpt-5.2-codex", tone: "ok", note: "healthy" },
  ]},
]

export function hashStr(s) {
  let h = 0
  for (const c of s) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return h
}

// ----- provider key pools (mock of masked keys from the backend) --------------

export const KEY_TONE = { healthy: "tone-ok", "rate-limited": "tone-warn", cooldown: "tone-warn", invalid: "tone-error" }

const KEY_SPECS = {
  google: { count: 3, bad: { 1: "cooldown" } },
  openai: { count: 2 },
  groq: { count: 2, bad: { 1: "rate-limited" } },
  cerebras: { count: 1, bad: { 0: "rate-limited" } },
  volcengine: { count: 1, bad: { 0: "invalid" } },
}

function maskKey(seedStr) {
  let h = hashStr(seedStr) || 1
  const chars = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
  let out = ""
  for (let i = 0; i < 4; i++) {
    h ^= h << 13; h >>>= 0; h ^= h >> 17; h ^= h << 5; h >>>= 0
    out += chars[h % chars.length]
  }
  return out
}

PROVIDERS.forEach((p) => {
  const spec = KEY_SPECS[p.name] ?? { count: 1 }
  p.keyList = Array.from({ length: spec.count }, (_, i) => {
    const status = spec.bad?.[i] ?? "healthy"
    const h = hashStr(p.name + ":" + i)
    return {
      id: `${p.name[0]}#${i + 1}`,
      masked: "…" + maskKey(p.name + ":" + i),
      status,
      err: status === "healthy" ? h % 30 : 300 + (h % 1700),
      on: status !== "invalid",
    }
  })
})

// ----- request logs (paged mock dataset) ---------------------------------------

export const LOGS = (() => {
  const handcrafted = [
    { time: "01:44:52", level: "error", msg: "all keys exhausted for gemini-3.8-flash via google: status=503", provider: "google", model: "gemini-3.8-flash", key: "g#2", ttft: "841 ms", tps: null, status: 502, ageH: 0 },
    { time: "01:44:44", level: "warn", msg: "upstream google transient (retry 3/3): status=503 ct=text/event-stream xrid=—", provider: "google", model: "gemini-3.8-flash", key: "g#2", ttft: "8,102 ms", tps: null, status: 503, ageH: 0 },
    { time: "01:44:44", level: "warn", msg: "upstream google transient (retry 2/3): status=503 ct=text/event-stream xrid=—", provider: "google", model: "gemini-3.8-flash", key: "g#2", ttft: "3,204 ms", tps: null, status: 503, ageH: 0 },
    { time: "01:44:41", level: "warn", msg: "upstream google transient (retry 1/3): status=503 ct=text/event-stream xrid=—", provider: "google", model: "gemini-3.8-flash", key: "g#2", ttft: "312 ms", tps: null, status: 503, ageH: 0 },
    { time: "01:44:39", level: "info", msg: "routed gpt-5.2-mini → openai (key o#1) · stream ok · first token 512 ms", provider: "openai", model: "gpt-5.2-mini", key: "o#1", ttft: "512 ms", tps: "61.4", status: 200, ageH: 0 },
    { time: "01:44:37", level: "info", msg: "routed gemini-3.8-flash → google (key g#1) · stream ok · first token 388 ms", provider: "google", model: "gemini-3.8-flash", key: "g#1", ttft: "388 ms", tps: "54.1", status: 200, ageH: 0 },
    { time: "01:44:31", level: "warn", msg: "upstream openai 429 rate_limited (retry 1/3) → backoff 800ms", provider: "openai", model: "gpt-5.2-mini", key: "o#1", ttft: "221 ms", tps: null, status: 429, ageH: 0 },
    { time: "01:44:30", level: "info", msg: "routed claude-sonnet-4.8 → anthropic (key a#1) · stream ok · first token 843 ms", provider: "anthropic", model: "claude-sonnet-4.8", key: "a#1", ttft: "843 ms", tps: "38.2", status: 200, ageH: 0 },
    { time: "01:44:22", level: "error", msg: "invalid api key for groq (key k#3): 401 unauthorized — key disabled 15m", provider: "groq", model: "llama-4-maverick", key: "k#3", ttft: "89 ms", tps: null, status: 401, ageH: 0 },
    { time: "01:44:19", level: "info", msg: "config reloaded (routerllm.yaml) · 15 providers · 15 models · no errors", provider: "—", model: "—", key: "—", ttft: "—", tps: null, status: null, ageH: 0 },
    { time: "01:44:11", level: "info", msg: "routed deepseek-v4 → deepseek (key d#1) · stream ok · first token 421 ms", provider: "deepseek", model: "deepseek-v4", key: "d#1", ttft: "421 ms", tps: "58.7", status: 200, ageH: 0 },
  ]

  const pairs = []
  MODELS.forEach((m) => m.legs.forEach((l) => pairs.push({ provider: l.route.split("/")[0], model: m.name, ttft: m.ttft })))

  const generated = []
  let sec = 44 * 60 + 8
  for (let i = 0; i < 35; i++) {
    const h = hashStr("log-" + i)
    const pair = pairs[h % pairs.length]
    sec -= 2 + (h % 8)
    const time = `01:${String(Math.floor(sec / 60)).padStart(2, "0")}:${String(sec % 60).padStart(2, "0")}`
    const key = `${pair.provider[0]}#${1 + (h % 2)}`
    const roll = h % 19
    if (roll < 13) {
      const ttft = pair.ttft + (h % 90) - 45
      generated.push({ time, level: "info", msg: `routed ${pair.model} → ${pair.provider} (key ${key}) · stream ok · first token ${ttft} ms`, provider: pair.provider, model: pair.model, key, ttft: `${ttft} ms`, tps: (28 + (h % 650) / 10).toFixed(1), status: 200, ageH: 0 })
    } else if (roll < 15) {
      generated.push({ time, level: "warn", msg: `upstream ${pair.provider} 429 rate_limited (retry 1/3) → backoff ${200 + (h % 7) * 100}ms`, provider: pair.provider, model: pair.model, key, ttft: `${120 + (h % 300)} ms`, tps: null, status: 429, ageH: 0 })
    } else if (roll < 17) {
      generated.push({ time, level: "warn", msg: `upstream ${pair.provider} transient (retry ${1 + (h % 2)}/3): status=503 ct=text/event-stream xrid=—`, provider: pair.provider, model: pair.model, key, ttft: `${900 + (h % 4000)} ms`, tps: null, status: 503, ageH: 0 })
    } else if (roll === 17) {
      generated.push({ time, level: "warn", msg: `upstream ${pair.provider} 429 rate_limited (retry 2/3) → backoff 1,600ms`, provider: pair.provider, model: pair.model, key, ttft: `${600 + (h % 500)} ms`, tps: null, status: 429, ageH: 0 })
    } else {
      generated.push({ time, level: "error", msg: `all keys exhausted for ${pair.model} via ${pair.provider}: status=503`, provider: pair.provider, model: pair.model, key, ttft: `${3000 + (h % 4000)} ms`, tps: null, status: 502, ageH: 0 })
    }
  }
  return [...handcrafted, ...generated, ...olderEntries()]
})()

// Sparse older rows so the time-range filter (1h/24h/7d/30d) has something to bite on.
function olderEntries() {
  return [
    { time: "Sep 6 09:14", level: "error", msg: "all keys exhausted for qwen3-max via volcengine: status=503", provider: "volcengine", model: "qwen3-max", key: "v#1", ttft: "5,210 ms", tps: null, status: 502, ageH: 40 },
    { time: "Sep 5 22:41", level: "info", msg: "routed claude-opus-4.6 → anthropic (key a#1) · stream ok · first token 1,162 ms", provider: "anthropic", model: "claude-opus-4.6", key: "a#1", ttft: "1,162 ms", tps: "24.8", status: 200, ageH: 51 },
    { time: "Sep 4 15:03", level: "warn", msg: "upstream groq 429 rate_limited (retry 3/3) → failover to together", provider: "groq", model: "llama-4-maverick", key: "k#2", ttft: "1,890 ms", tps: null, status: 429, ageH: 83 },
    { time: "Sep 3 08:27", level: "info", msg: "routed gpt-5.2 → azure-openai (key z#1) · stream ok · first token 741 ms", provider: "azure-openai", model: "gpt-5.2", key: "z#1", ttft: "741 ms", tps: "33.5", status: 200, ageH: 113 },
    { time: "Sep 1 19:52", level: "error", msg: "invalid api key for cerebras (key c#1): 401 unauthorized — key disabled 15m", provider: "cerebras", model: "qwen3-max", key: "c#1", ttft: "74 ms", tps: null, status: 401, ageH: 141 },
    { time: "Aug 30 11:36", level: "info", msg: "routed llama-4-scout → together (key t#1) · stream ok · first token 271 ms", provider: "together", model: "llama-4-scout", key: "t#1", ttft: "271 ms", tps: "66.9", status: 200, ageH: 166 },
    { time: "Aug 25 13:08", level: "warn", msg: "upstream bedrock transient (retry 1/3): status=503 xrid=—", provider: "bedrock", model: "claude-opus-4.6", key: "b#1", ttft: "2,431 ms", tps: null, status: 503, ageH: 308 },
    { time: "Aug 12 07:44", level: "info", msg: "routed mistral-large-3 → mistral (key m#1) · stream ok · first token 452 ms", provider: "mistral", model: "mistral-large-3", key: "m#1", ttft: "452 ms", tps: "47.3", status: 200, ageH: 624 },
  ]
}

export function providerModelRows(provider) {
  const rows = []
  MODELS.forEach((m) => {
    m.legs.forEach((leg, i) => {
      if (!leg.route.startsWith(provider + "/")) return
      const baseReq = parseFloat(m.req) * 1000
      const share = leg.tone === "error" ? 0.04 : leg.tone === "warn" ? 0.6 : i === 0 ? 0.85 : 0.15
      const h = hashStr(leg.route)
      const p50 = Math.round(m.ttft * (0.9 + (h % 25) / 100))
      rows.push({
        model: m.name,
        modelId: leg.route.slice(provider.length + 1),
        route: leg.route,
        tone: leg.tone,
        req: Math.max(120, Math.round(baseReq * share)),
        p50,
        p95: Math.round(p50 * (2.3 + (h % 7) * 0.15)),
        tps: Math.max(24, Math.round(96 - p50 / 22)),
      })
    })
  })

  return rows.sort((a, b) => b.req - a.req)
}

export const PROVIDER_AGG = {}
PROVIDERS.forEach((p) => {
  const rows = providerModelRows(p.name)
  const totalReq = rows.reduce((s, r) => s + r.req, 0)
  PROVIDER_AGG[p.name] = totalReq ? Math.round(rows.reduce((s, r) => s + r.req * r.p50, 0) / totalReq) : null
})

export const TRAFFIC = [
  ["12:00", "82,140 req · 412 err (0.5%)"], ["14:00", "88,320 req · 388 err (0.4%)"],
  ["16:00", "95,410 req · 455 err (0.5%)"], ["18:00", "104,220 req · 502 err (0.5%)"],
  ["20:00", "97,180 req · 431 err (0.4%)"], ["22:00", "91,050 req · 398 err (0.4%)"],
  ["00:00", "86,340 req · 377 err (0.4%)"], ["02:00", "58,410 req · 3,214 err (5.5%) · google 503"],
  ["04:00", "42,880 req · 289 err (0.7%)"], ["06:00", "49,650 req · 302 err (0.6%)"],
  ["08:00", "67,290 req · 356 err (0.5%)"], ["now", "84,770 req · 405 err (0.5%)"],
]

export const WEEK = ["Sep 1", "Sep 2", "Sep 3", "Sep 4", "Sep 5", "Sep 6", "Today"]
export const SUCCESS = ["99.1%", "99.0%", "99.2%", "99.0%", "97.1% · google 503 incident", "98.8%", "99.0%"]
export const TTFT_7D = [
  [398, 1240, 2410], [402, 1210, 2380], [395, 1265, 2455], [401, 1232, 2402],
  [512, 1980, 4120], [399, 1225, 2390], [396, 1218, 2375],
]
