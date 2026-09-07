import { MODELS, PROVIDERS } from "./data.js"

// Mutable mock state so toggles/reorders act like a real backend.
// Persisted to localStorage so "เปลี่ยนได้จริง" survives a refresh.
// Swap this module for api.js calls when the backend is wired.

const KEY = "routerllm.admin.mockstate.v2"

function seed() {
  const providers = structuredClone(PROVIDERS)
  const models = structuredClone(MODELS)
  models.forEach((m) =>
    m.legs.forEach((leg) => {
      leg.on = true
      leg.baseNote = leg.note
      leg.baseTone = leg.tone
    })
  )
  return { providers, models }
}

function load() {
  try {
    const raw = localStorage.getItem(KEY)
    if (raw) {
      const parsed = JSON.parse(raw)
      if (parsed?.providers?.length && parsed?.models?.length) return parsed
    }
  } catch {
    // fall through to seed
  }
  return seed()
}

let state = load()
const listeners = new Set()

function commit() {
  try { localStorage.setItem(KEY, JSON.stringify(state)) } catch { /* storage full/blocked — keep in-memory */ }
  listeners.forEach((fn) => fn(state))
}

export function getState() {
  return state
}

export function subscribe(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export function toggleProvider(name, on) {
  const p = state.providers.find((x) => x.name === name)
  if (!p) return
  p.on = on
  p.status = on ? "healthy" : "disabled"
  p.up = on ? p.up ?? "99.0%" : "—"
  commit()
}

export function toggleModel(name, on) {
  const m = state.models.find((x) => x.name === name)
  if (!m) return
  m.on = on
  commit()
}

export function toggleLeg(modelName, index, on) {
  const m = state.models.find((x) => x.name === modelName)
  const leg = m?.legs[index]
  if (!leg) return
  leg.on = on
  leg.note = on ? leg.baseNote : "disabled"
  leg.tone = on ? leg.baseTone : "info"
  commit()
}

export function moveLeg(modelName, index, direction) {
  const m = state.models.find((x) => x.name === modelName)
  if (!m) return
  const target = direction === "up" ? index - 1 : index + 1
  if (target < 0 || target >= m.legs.length) return
  ;[m.legs[index], m.legs[target]] = [m.legs[target], m.legs[index]]
  commit()
}

export function removeLeg(modelName, index) {
  const m = state.models.find((x) => x.name === modelName)
  if (!m || m.legs.length <= 1) return
  m.legs.splice(index, 1)
  commit()
}

export function addLeg(modelName, provider, opts = {}) {
  const m = state.models.find((x) => x.name === modelName)
  if (!m) return
  m.legs.push({
    route: `${provider}/${opts.upstreamModel || m.name}`,
    effort: opts.effort ?? null,
    tone: "info",
    note: "standby",
    on: true,
    baseNote: "standby",
    baseTone: "info",
  })
  commit()
}

export function toggleKey(providerName, index, on) {
  const p = state.providers.find((x) => x.name === providerName)
  const key = p?.keyList?.[index]
  if (!key) return
  key.on = on
  if (!p.keyList.some((k) => k.on) && p.on) {
    p.on = false
    p.status = "disabled"
    p.up = "—"
  }
  commit()
}
