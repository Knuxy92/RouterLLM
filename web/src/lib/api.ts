import { hmacProof } from "./hmac"
import type {
  AddLegBody,
  AuthChallenge,
  AuthVerify,
  LogsPagePayload,
  MetricsPayload,
  ModelCatalog,
  PulsePayload,
  RequestsDelta,
  RuntimeConfig,
  StatusPayload,
  TestProviderBody,
  TestProviderResult,
} from "./types"

// Session id lives in localStorage; the admin secret is never stored or sent —
// only the HMAC proof, once per login.

const SESSION_KEY = "routerllm.admin.session"
const BASE = "/admin/api"

const memoryStore = new Map<string, string>()

export function readLocal(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return memoryStore.get(key) ?? null
  }
}

export function writeLocal(key: string, value: string): void {
  try {
    localStorage.setItem(key, value)
  } catch {
    memoryStore.set(key, value)
  }
}

export function removeLocal(key: string): void {
  try {
    localStorage.removeItem(key)
  } catch {
    memoryStore.delete(key)
  }
}

export function getToken(): string | null {
  return readLocal(SESSION_KEY)
}

export function setToken(token: string): void {
  writeLocal(SESSION_KEY, token)
}

export function clearToken(): void {
  removeLocal(SESSION_KEY)
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
    this.name = "ApiError"
  }
}

function errorMessage(payload: unknown, fallback: string): string {
  if (payload && typeof payload === "object") {
    const record = payload as Record<string, unknown>
    const nested = record.error as Record<string, unknown> | undefined
    const msg = nested?.message ?? record.message
    if (typeof msg === "string" && msg) return msg
  }
  return fallback
}

export interface RequestInitLike {
  method?: string
  headers?: Record<string, string>
  body?: string
}

export async function call<T>(path: string, init: RequestInitLike = {}): Promise<T> {
  const headers: Record<string, string> = { ...(init.headers || {}) }
  const token = getToken()
  const hadToken = Boolean(token)
  if (token) headers.Authorization = "Bearer " + token

  if (init.body !== undefined && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json"
  }

  const res = await fetch(BASE + path, { ...init, headers })

  let payload: unknown = null
  try {
    payload = await res.json()
  } catch {
    // non-JSON body (proxy error page etc.) — fall through to status handling
  }

  if (res.status === 401 && hadToken && path !== "/auth/challenge" && path !== "/auth/verify") {
    clearToken()
    window.dispatchEvent(new Event("routerllm:unauthorized"))
    throw new ApiError(401, errorMessage(payload, "session expired — sign in again"))
  }

  if (!res.ok) {
    throw new ApiError(res.status, errorMessage(payload, res.status + " " + res.statusText))
  }

  return payload as T
}

export async function post<T>(path: string, body?: unknown): Promise<T> {
  return call<T>(path, { method: "POST", body: JSON.stringify(body ?? {}) })
}

export interface RequestsPageParams {
  page?: number | string
  perPage?: number | string
  provider?: string
  model?: string
  level?: string
  q?: string
  hours?: number | string
}

/** Full login handshake: nonce in, HMAC proof back, session id stored. */
export async function login(secret: string): Promise<AuthVerify> {
  const ch = await call<AuthChallenge>("/auth/challenge", { method: "POST" })
  const proof = hmacProof(secret, ch.nonce)
  const result = await call<AuthVerify>("/auth/verify", {
    method: "POST",
    body: JSON.stringify({ challenge_id: ch.challenge_id, proof }),
  })
  setToken(result.session)
  return result
}

export const api = {
  login,
  get: <T>(path: string): Promise<T> => call<T>(path),
  post,
  status: () => call<StatusPayload>("/status"),
  metrics: () => call<MetricsPayload>("/metrics"),
  pulse: () => call<PulsePayload>("/pulse"),
  /** Console tunables (poll cadence, page size, test clamps, whitelists). */
  config: () => call<RuntimeConfig>("/config"),
  /** Delta mode: raw events after <since> (seq), plus the store's latest seq. */
  requestsSince: (since = 0) => call<RequestsDelta>("/requests?since=" + since),
  /** Page mode: {page, perPage, provider, model, level, q, hours}. */
  requestsPage: (p: RequestsPageParams = {}) => {
    const qs = new URLSearchParams()
    if (p.page) qs.set("page", String(p.page))
    if (p.perPage) qs.set("per_page", String(p.perPage))
    if (p.provider) qs.set("provider", p.provider)
    if (p.model) qs.set("model", p.model)
    if (p.level) qs.set("level", p.level)
    if (p.q) qs.set("q", p.q)
    if (p.hours) qs.set("hours", String(p.hours))
    const s = qs.toString()
    return call<LogsPagePayload>("/requests" + (s ? "?" + s : ""))
  },
  logout: () => post<{ ok: boolean }>("/auth/logout"),
  reload: () => post<StatusPayload>("/reload"),
  setProviderDisabled: (name: string, disabled: boolean) =>
    post<StatusPayload>("/providers/" + encodeURIComponent(name), { disabled }),
  setKeyDisabled: (name: string, index: number, disabled: boolean) =>
    post<StatusPayload>(`/providers/${encodeURIComponent(name)}/keys/${index}`, { disabled }),
  setModelDisabled: (model: string, disabled: boolean) =>
    post<StatusPayload>("/routes/" + encodeURIComponent(model), { disabled }),
  setRouteDisabled: (model: string, index: number, disabled: boolean) =>
    post<StatusPayload>(`/routes/${encodeURIComponent(model)}/${index}`, { disabled }),
  moveRoute: (model: string, index: number, direction: string) =>
    post<StatusPayload>("/routes/" + encodeURIComponent(model) + "/move", { index, direction }),
  addRouteLeg: (model: string, leg: AddLegBody) =>
    post<StatusPayload>("/routes/" + encodeURIComponent(model) + "/add", leg),
  removeRouteLeg: (model: string, index: number) =>
    post<StatusPayload>("/routes/" + encodeURIComponent(model) + "/remove", { index }),
  testProvider: (name: string, body: TestProviderBody) =>
    post<TestProviderResult>("/providers/" + encodeURIComponent(name) + "/test", body),
  providerModels: (name: string) =>
    call<ModelCatalog>("/providers/" + encodeURIComponent(name) + "/models"),
}
