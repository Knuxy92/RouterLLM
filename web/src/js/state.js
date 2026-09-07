import { api } from "./api.js"
import { adaptModels, adaptProviders } from "./data.js"

// Server-backed state. Nothing is persisted locally — /status, /metrics and
// /requests are polled every 3s and every mutation goes through the admin API.
// Toggles are optimistic: the local status copy is mutated immediately, then
// the API result (which is itself a fresh Status) replaces it; on failure the
// state is rolled back by re-fetching /status.

const POLL_MS = 3000
const RING_CAP = 2000

let lastStatus = null
let lastMetrics = null
let requestEntries = []
let lastReqSeq = 0
let timer = null
let polling = false

const listeners = new Set()

export function getStatus() {
  return lastStatus
}

export function getMetrics() {
  return lastMetrics
}

export function getRequests() {
  return requestEntries
}

export function getState() {
  return {
    providers: adaptProviders(lastStatus, lastMetrics),
    models: adaptModels(lastStatus, lastMetrics),
  }
}

export function subscribe(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

function notify() {
  listeners.forEach((fn) => fn())
}

function mergeRequests(entries, latest) {
  if (latest < lastReqSeq) {
    // Server restarted (seq counter reset) — drop the stale tail and resync.
    requestEntries = []
    lastReqSeq = 0
    return false
  }
  if (entries?.length) {
    requestEntries = requestEntries.concat(entries).slice(-RING_CAP)
  }
  lastReqSeq = latest
  return true
}

async function pollOnce() {
  if (polling) return
  polling = true

  try {
    const [status, metrics, reqs] = await Promise.all([
      api.status(),
      api.metrics(),
      api.requests(lastReqSeq),
    ])

    lastStatus = status
    lastMetrics = metrics
    if (!mergeRequests(reqs.entries, reqs.latest) ) {
      const fresh = await api.requests(0)
      mergeRequests(fresh.entries, fresh.latest)
    }

    notify()
  } catch {
    // 401 is handled globally by api.js (unauthorized event); transient
    // network/5xx failures just wait for the next tick with the old state.
  } finally {
    polling = false
  }
}

export function startPolling() {
  stopPolling()
  pollOnce()
  timer = setInterval(() => {
    if (!document.hidden) pollOnce()
  }, POLL_MS)
}

export function stopPolling() {
  if (timer) clearInterval(timer)
  timer = null
}

// ----- mutations -----------------------------------------------------------------

async function mutate(optimistic, request) {
  if (lastStatus) {
    optimistic(lastStatus)
    notify()
  }

  try {
    lastStatus = await request()
  } catch {
    try {
      lastStatus = await api.status()
    } catch {
      // keep the optimistic state if even the rollback fetch fails; the next
      // poll tick will resync anyway
    }
  }

  notify()
}

function findProvider(status, name) {
  return status.providers?.find((p) => p.name === name)
}

function findModel(status, name) {
  return status.models?.find((m) => m.model_id === name)
}

export function toggleProvider(name, on) {
  mutate(
    (s) => {
      const p = findProvider(s, name)
      if (p) p.disabled = !on
    },
    () => api.setProviderDisabled(name, !on),
  )
}

export function toggleModel(name, on) {
  mutate(
    (s) => {
      const m = findModel(s, name)
      if (m) m.disabled = !on
    },
    () => api.setModelDisabled(name, !on),
  )
}

export function toggleLeg(modelName, index, on) {
  mutate(
    (s) => {
      const leg = findModel(s, modelName)?.chain?.[index]
      if (leg) leg.disabled = !on
    },
    () => api.setRouteDisabled(modelName, index, !on),
  )
}

export function moveLeg(modelName, index, direction) {
  mutate(
    (s) => {
      const chain = findModel(s, modelName)?.chain
      if (!chain) return
      const target = direction === "up" ? index - 1 : index + 1
      if (target < 0 || target >= chain.length) return
      ;[chain[index], chain[target]] = [chain[target], chain[index]]
    },
    () => api.moveRoute(modelName, index, direction),
  )
}

export function removeLeg(modelName, index) {
  const chain = findModel(lastStatus ?? {}, modelName)?.chain
  if (!chain || chain.length <= 1) return
  mutate(
    (s) => {
      s.models?.find((m) => m.model_id === modelName)?.chain?.splice(index, 1)
    },
    () => api.removeRouteLeg(modelName, index),
  )
}

export function addLeg(modelName, provider, opts = {}) {
  const upstream = opts.upstreamModel || modelName
  mutate(
    (s) => {
      findModel(s, modelName)?.chain?.push({
        provider,
        model: upstream,
        disabled: !!opts.disabled,
        active: false,
        provider_disabled: false,
      })
    },
    () => api.addRouteLeg(modelName, {
      provider,
      model: upstream,
      reasoning_effort: opts.effort || undefined,
      disabled: !!opts.disabled,
    }),
  )
}

export function toggleKey(providerName, index, on) {
  mutate(
    (s) => {
      const key = findProvider(s, providerName)?.keys?.[index]
      if (key) key.disabled = !on
    },
    () => api.setKeyDisabled(providerName, index, !on),
  )
}

export async function reloadConfig() {
  try {
    lastStatus = await api.reload()
  } catch {
    try {
      lastStatus = await api.status()
    } catch {
      // next poll tick resyncs
    }
  }

  notify()
}
