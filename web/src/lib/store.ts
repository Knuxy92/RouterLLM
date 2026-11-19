import { api } from "./api"
import { adaptLogs, adaptModels, adaptProviders } from "./data"
import type {
  LegInput,
  LogEvent,
  LogFilters,
  LogsPage,
  MetricsPayload,
  RequestsDelta,
  RuntimeConfig,
  StatusPayload,
  StoreState,
} from "./types"

// Server-backed external store. The poll loop hits /pulse every POLL_MS and
// re-fetches /status, /metrics or the request log only when its signature or
// cursor moved; new requests arrive as ?since= deltas into a bounded ring.
// /status uptime is extrapolated client-side so a frozen payload still ticks.
// Toggles are optimistic — the local copy mutates immediately, then is replaced
// by the API's fresh Status; on failure it rolls back by re-fetching /status.

const DEFAULT_CONFIG: RuntimeConfig = {
  ui: { poll_ms: 3000, log_per_page: 20 },
  test: {
    prompt_bytes: 4000,
    max_tokens: 8192,
    default_tokens: 256,
    timeout_floor: 5,
    timeout_ceiling: 120,
    default_timeout: 20,
    default_prompt: "Say 'pong' and nothing else.",
  },
  efforts: ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"],
  stylecalls: ["chat", "responses", "messages"],
  ring_capacity: 2000,
}

export interface LoadLogsOptions {
  prefetch?: boolean
}

export interface AdminStore {
  getSnapshot(): StoreState
  subscribe(listener: () => void): () => void
  init(): Promise<void>
  startPolling(): void
  stopPolling(): void
  getStatus(): StatusPayload | null
  getMetrics(): MetricsPayload | null
  getConfig(): RuntimeConfig
  getLogsPage(): LogsPage
  getLogFilters(): LogFilters
  getRecentFailures(): LogEvent[]
  getRequests(): LogEvent[]
  getUptimeSeconds(): number | null
  getView(): { providers: ReturnType<typeof adaptProviders>; models: ReturnType<typeof adaptModels> }
  resetState(): void
  toggleProvider(name: string, disabled: boolean): Promise<void>
  toggleModel(model: string, disabled: boolean): Promise<void>
  toggleLeg(model: string, index: number, disabled: boolean): Promise<void>
  toggleKey(provider: string, index: number, disabled: boolean): Promise<void>
  addLeg(model: string, leg: LegInput): Promise<void>
  removeLeg(model: string, index: number): Promise<void>
  moveLeg(model: string, index: number, direction: -1 | 1): Promise<void>
  reloadConfig(): Promise<void>
  setLogFilter(patch: Partial<LogFilters>, page?: number): Promise<void>
  loadLogsPage(pageNo: number, options?: LoadLogsOptions): Promise<void>
}

export function createAdminStore(): AdminStore {
  let runtimeConfig: RuntimeConfig = DEFAULT_CONFIG
  let pollMs = DEFAULT_CONFIG.ui.poll_ms
  let ringCap = DEFAULT_CONFIG.ring_capacity

  let lastStatus: StatusPayload | null = null
  let lastMetrics: MetricsPayload | null = null
  let lastStatusAt = 0
  let statusSig = ""
  let metricsSig = ""
  let cursor = 0
  let ring: LogEvent[] = []
  let timer: ReturnType<typeof setInterval> | null = null
  let polling = false

  let logMeta: LogsPage["meta"] = {
    page: 1,
    per_page: DEFAULT_CONFIG.ui.log_per_page,
    total: 0,
    total_pages: 1,
    error_total: 0,
    latest: 0,
  }
  let logPageCache = new Map<number, { entries: LogEvent[] }>()
  let logFilters: LogFilters = { provider: "", model: "", level: "", q: "", hours: 24, paused: false }
  let recentFailures: LogEvent[] = []
  let logGen = 0

  const listeners = new Set<() => void>()

  function getUptimeSeconds(): number | null {
    const base = lastStatus?.uptime_seconds
    if (base == null) return null

    return base + Math.max(0, Math.round((Date.now() - lastStatusAt) / 1000))
  }

  function buildSnapshot(): StoreState {
    return {
      status: lastStatus,
      metrics: lastMetrics,
      config: runtimeConfig,
      requests: ring,
      recentFailures,
      logs: { entries: logPageCache.get(logMeta.page)?.entries || [], meta: logMeta },
      filters: logFilters,
      uptimeSeconds: getUptimeSeconds(),
    }
  }

  let snapshot = buildSnapshot()

  function notify(): void {
    snapshot = buildSnapshot()
    listeners.forEach((fn) => fn())
  }

  function filterParams() {
    const p: Record<string, string | number> = { page: logMeta.page, perPage: logMeta.per_page }
    if (logFilters.provider) p.provider = logFilters.provider
    if (logFilters.model) p.model = logFilters.model
    if (logFilters.level) p.level = logFilters.level
    if (logFilters.q) p.q = logFilters.q
    if (logFilters.hours) p.hours = logFilters.hours
    return p
  }

  async function fetchPage(pageNo: number) {
    const gen = logGen
    const p = filterParams()
    p.page = pageNo
    const res = await api.requestsPage(p)
    if (gen !== logGen) return undefined
    logPageCache.set(pageNo, { entries: res.entries || [] })
    return res
  }

  async function loadLogsPage(pageNo: number, options: LoadLogsOptions = {}): Promise<void> {
    const { prefetch = true } = options
    const gen = logGen
    const res = await fetchPage(pageNo)
    if (gen !== logGen || !res) return
    logMeta = { ...logMeta, ...res }
    notify()
    if (prefetch) {
      for (const n of [pageNo - 1, pageNo + 1, pageNo + 2, pageNo - 2]) {
        if (n >= 1 && n <= logMeta.total_pages && !logPageCache.has(n)) {
          fetchPage(n).catch(() => {})
        }
      }
    }
  }

  function appendEvents(current: LogEvent[], fresh: LogEvent[]): LogEvent[] {
    const next = current.concat(fresh)
    return next.length > ringCap ? next.slice(next.length - ringCap) : next
  }

  function deriveRecentFailures(): void {
    const rows = adaptLogs(ring).filter((r) => r.level !== "info")
    recentFailures = rows.slice(0, 5).map((r) => r.entry)
  }

  async function initConfig(): Promise<void> {
    try {
      const remote = await api.config()
      runtimeConfig = {
        ...DEFAULT_CONFIG,
        ...remote,
        ui: { ...DEFAULT_CONFIG.ui, ...remote?.ui },
        test: { ...DEFAULT_CONFIG.test, ...remote?.test },
      }
    } catch {
      runtimeConfig = DEFAULT_CONFIG
    }

    pollMs = runtimeConfig.ui.poll_ms
    ringCap = runtimeConfig.ring_capacity
    logMeta = { ...logMeta, per_page: runtimeConfig.ui.log_per_page }
    notify()
  }

  async function pollOnce(): Promise<void> {
    if (polling) return
    polling = true

    try {
      const pulse = await api.pulse()

      if (pulse.seq < cursor) {
        ring = []
        recentFailures = []
        cursor = 0
        statusSig = ""
        metricsSig = ""
        lastStatus = null
        lastMetrics = null
      }

      const jobs: Promise<void>[] = []
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
      jobs.push(api.requestsSince(cursor).then((res: RequestsDelta) => {
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
      // 401 is handled globally by api.ts; transient failures wait for the next tick.
    } finally {
      polling = false
    }
  }

  function startPolling(): void {
    stopPolling()
    void pollOnce()
    timer = setInterval(() => {
      if (typeof document === "undefined" || !document.hidden) void pollOnce()
    }, pollMs)
  }

  function stopPolling(): void {
    if (timer) clearInterval(timer)
    timer = null
  }

  function resetState(): void {
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
    notify()
  }

  async function init(): Promise<void> {
    await initConfig()
  }

  async function mutate(
    optimistic: (status: StatusPayload) => void,
    request: () => Promise<StatusPayload>,
  ): Promise<void> {
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
        // keep the optimistic state if even the rollback fails; the next tick resyncs
      }
    }

    notify()
  }

  function findProvider(status: StatusPayload, name: string) {
    return status.providers?.find((p) => p.name === name)
  }

  function findModel(status: StatusPayload, name: string) {
    return status.models?.find((m) => m.model_id === name)
  }

  async function toggleProvider(name: string, disabled: boolean): Promise<void> {
    await mutate(
      (s) => {
        const p = findProvider(s, name)
        if (p) p.disabled = disabled
      },
      () => api.setProviderDisabled(name, disabled),
    )
  }

  async function toggleModel(model: string, disabled: boolean): Promise<void> {
    await mutate(
      (s) => {
        const m = findModel(s, model)
        if (m) m.disabled = disabled
      },
      () => api.setModelDisabled(model, disabled),
    )
  }

  async function toggleLeg(model: string, index: number, disabled: boolean): Promise<void> {
    await mutate(
      (s) => {
        const leg = findModel(s, model)?.chain?.[index]
        if (leg) leg.disabled = disabled
      },
      () => api.setRouteDisabled(model, index, disabled),
    )
  }

  async function toggleKey(provider: string, index: number, disabled: boolean): Promise<void> {
    await mutate(
      (s) => {
        const key = findProvider(s, provider)?.keys?.[index]
        if (key) key.disabled = disabled
      },
      () => api.setKeyDisabled(provider, index, disabled),
    )
  }

  async function moveLeg(model: string, index: number, direction: -1 | 1): Promise<void> {
    await mutate(
      (s) => {
        const chain = findModel(s, model)?.chain
        if (!chain) return
        const target = index + direction
        if (target < 0 || target >= chain.length) return
        ;[chain[index], chain[target]] = [chain[target], chain[index]]
      },
      () => api.moveRoute(model, index, direction === -1 ? "up" : "down"),
    )
  }

  async function removeLeg(model: string, index: number): Promise<void> {
    const chain = findModel(lastStatus ?? ({} as StatusPayload), model)?.chain
    if (!chain || chain.length <= 1) return
    await mutate(
      (s) => {
        s.models?.find((m) => m.model_id === model)?.chain?.splice(index, 1)
      },
      () => api.removeRouteLeg(model, index),
    )
  }

  async function addLeg(model: string, leg: LegInput): Promise<void> {
    const upstream = leg.model || model
    await mutate(
      (s) => {
        findModel(s, model)?.chain?.push({
          provider: leg.provider,
          model: upstream,
          disabled: !!leg.disabled,
          active: false,
          provider_disabled: false,
          stylecall: leg.stylecall || null,
        })
      },
      () => api.addRouteLeg(model, {
        provider: leg.provider,
        model: upstream,
        reasoning_effort: leg.effort || undefined,
        disabled: !!leg.disabled,
        stylecall: leg.stylecall || undefined,
      }),
    )
  }

  async function reloadConfig(): Promise<void> {
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

  async function setLogFilter(patch: Partial<LogFilters>, page = 1): Promise<void> {
    logGen++
    logFilters = { ...logFilters, ...patch }
    logPageCache = new Map()
    logMeta = { ...logMeta, page, total: 0, total_pages: 1 }
    await loadLogsPage(page, { prefetch: false })
  }

  if (typeof window !== "undefined") {
    window.addEventListener("routerllm:unauthorized", () => {
      stopPolling()
      resetState()
    })
  }

  return {
    getSnapshot: () => snapshot,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    init,
    startPolling,
    stopPolling,
    getStatus: () => lastStatus,
    getMetrics: () => lastMetrics,
    getConfig: () => runtimeConfig,
    getLogsPage: () => ({ entries: logPageCache.get(logMeta.page)?.entries || [], meta: logMeta }),
    getLogFilters: () => logFilters,
    getRecentFailures: () => recentFailures,
    getRequests: () => ring,
    getUptimeSeconds,
    getView: () => ({
      providers: adaptProviders(lastStatus, lastMetrics),
      models: adaptModels(lastStatus, lastMetrics),
    }),
    resetState,
    toggleProvider,
    toggleModel,
    toggleLeg,
    toggleKey,
    addLeg,
    removeLeg,
    moveLeg,
    reloadConfig,
    setLogFilter,
    loadLogsPage,
  }
}

/** Shared instance for React islands; call init() once and startPolling() on mount. */
export const adminStore = createAdminStore()
