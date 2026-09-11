import { api } from "./api.js"
import { adaptLogs, adaptModels, adaptProviders } from "./data.js"

// Server-backed state. The poll loop hits /pulse (a ~100-byte change-signature
// heartbeat) every 3s and re-fetches /status, /metrics or the request log only
// when its signature or cursor moved; new requests arrive as ?since= deltas
// into a client-side ring. /status's uptime is extrapolated client-side so a
// frozen payload still ticks. Toggles are optimistic — the local copy mutates
// immediately, then is replaced by the API's fresh Status; on failure the
// state rolls back by re-fetching /status.

const POLL_MS = 3000
const RING_CAP = 2000

let lastStatus = null
let lastMetrics = null
let lastStatusAt = 0
let statusSig = ""
let metricsSig = ""
let cursor = 0
let ring = []
let timer = null
let polling = false

// ----- request-log paging ---------------------------------------------------------
// Filtered page views are server-sliced as before (page nav, filters, export).
// Live updates flow through the delta ring: page 1 is re-fetched only when new
// events actually arrived, and pages >1 stay put while you browse them.

export const LOG_PER_PAGE = 20

let logMeta = { page: 1, per_page: LOG_PER_PAGE, total: 0, total_pages: 1, error_total: 0, latest: 0 }
let logPageCache = new Map()
let logFilters = { provider: "", model: "", level: "", q: "", hours: 24, paused: false }
let recentFailures = []

// Bumped on every filter change; in-flight page fetches compare their captured
// generation against it and discard results that no longer match the filters.
let logGen = 0

const listeners = new Set()

export function getStatus() {
  return lastStatus
}

export function getMetrics() {
  return lastMetrics
}

export function getLogsPage() {
  return { entries: (logPageCache.get(logMeta.page)?.entries) || [], meta: logMeta }
}

export function getLogFilters() {
  return logFilters
}

export function getRecentFailures() {
  return recentFailures
}

/** Newest-first delta-ring view (capped at RING_CAP) for cmd+k and traces. */
export function getRequests() {
  return ring
}

export function getUptimeSeconds() {
  const base = lastStatus?.uptime_seconds
  if (base == null) return null

  return base + Math.max(0, Math.round((Date.now() - lastStatusAt) / 1000))
}

function filterParams() {
  const p = { page: logMeta.page, perPage: LOG_PER_PAGE }
  if (logFilters.provider) p.provider = logFilters.provider
  if (logFilters.model) p.model = logFilters.model
  if (logFilters.level) p.level = logFilters.level
  if (logFilters.q) p.q = logFilters.q
  if (logFilters.hours) p.hours = logFilters.hours
  return p
}

async function fetchPage(pageNo) {
  const gen = logGen
  const p = filterParams()
  p.page = pageNo
  const res = await api.requestsPage(p)
  if (gen !== logGen) return
  logPageCache.set(pageNo, { pageNo, entries: res.entries || [] })
  return res
}

/** Change one filter (or jump pages) and reload page 1 / the given page. */
export async function setLogFilter(patch, page = 1) {
  logGen++
  logFilters = { ...logFilters, ...patch }
  logPageCache = new Map()
  logMeta = { ...logMeta, page, total: 0, total_pages: 1 }
  await loadLogsPage(page, { prefetch: false })
}

export async function loadLogsPage(pageNo, { prefetch = true } = {}) {
  const gen = logGen
  const res = await fetchPage(pageNo)
  if (gen !== logGen || !res) return
  logMeta = { ...logMeta, ...res }
  notify()
  if (prefetch) {
    // warm the neighbours so paging feels instant; failures are silent
    for (const n of [pageNo - 1, pageNo + 1, pageNo + 2, pageNo - 2]) {
      if (n >= 1 && n <= logMeta.total_pages && !logPageCache.has(n)) {
        fetchPage(n).catch(() => {})
      }
    }
  }
}

function appendEvents(ring, fresh) {
  const next = ring.concat(fresh)

  return next.length > RING_CAP ? next.slice(next.length - RING_CAP) : next
}

function deriveRecentFailures() {
  const rows = adaptLogs(ring).filter((r) => r.level !== "info")
  recentFailures = rows.slice(0, 5).map((r) => r.entry)
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

async function pollOnce() {
  if (polling) return
  polling = true

  try {
    const pulse = await api.pulse()

    if (pulse.seq < cursor) {
      // the store reset (restart with a replay that lost events) — resync
      // everything from scratch on this tick
      ring = []
      recentFailures = []
      cursor = 0
      statusSig = ""
      metricsSig = ""
      lastStatus = null
      lastMetrics = null
    }

    const jobs = []
    let dirty = false
    let newCount = 0

    if (!lastStatus || pulse.status_sig !== statusSig) {
      jobs.push(api.status().then((s) => {
        lastStatus = s
        lastStatusAt = Date.now()
        statusSig = pulse.status_sig
        dirty = true
      }))
    }
    if (!lastMetrics || pulse.metrics_sig !== metricsSig) {
      jobs.push(api.metrics().then((m) => {
        lastMetrics = m
        metricsSig = pulse.metrics_sig
        dirty = true
      }))
    }
    jobs.push(api.requestsSince(cursor).then((res) => {
      const fresh = res.entries || []
      cursor = res.latest ?? pulse.seq
      if (fresh.length) {
        ring = appendEvents(ring, fresh)
        newCount = fresh.length
      }
    }))

    await Promise.all(jobs)

    if (newCount > 0) {
      dirty = true
      deriveRecentFailures()
      if (!logFilters.paused && logMeta.page === 1 && logPageCache.has(1))
        await loadLogsPage(1, { prefetch: false })
    }
    if (!logFilters.paused && !logPageCache.has(logMeta.page))
      await loadLogsPage(logMeta.page, { prefetch: false })

    if (dirty) notify()
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

/** Drop every server-derived value so a signed-out session cannot leak into the next one. */
export function resetState() {
  ring = []
  recentFailures = []
  logPageCache = new Map()
  logMeta = { ...logMeta, page: 1, total: 0, total_pages: 1, error_total: 0, latest: 0 }
  cursor = 0
  lastStatus = null
  lastMetrics = null
  lastStatusAt = 0
  statusSig = ""
  metricsSig = ""
}

// ----- mutations -----------------------------------------------------------------

async function mutate(optimistic, request) {
  if (lastStatus) {
    optimistic(lastStatus)
    notify()
  }

  try {
    lastStatus = await request()
    lastStatusAt = Date.now()
  } catch {
    try {
      lastStatus = await api.status()
      lastStatusAt = Date.now()
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
    lastStatusAt = Date.now()
  } catch {
    try {
      lastStatus = await api.status()
      lastStatusAt = Date.now()
    } catch {
      // next poll tick resyncs
    }
  }

  notify()
}
