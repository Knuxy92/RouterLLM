export type KeyState = {
  masked: string
  alive: boolean
  cooldown_left_seconds: number
}

export type ProviderStatus = {
  name: string
  style: string
  base_url: string
  auth_mode: string
  disabled: boolean
  keys_alive: number
  keys_total: number
  keys: KeyState[]
  requests: number
  errors: number
  share?: string
  serving: boolean
  model_count: number
}

export type RouteLeg = {
  provider: string
  model: string
  disabled: boolean
  active: boolean
  provider_disabled: boolean
}

export type ModelStatus = {
  model_id: string
  serving: boolean
  chain: RouteLeg[]
}

export type ReloadStatus = {
  at: string
  ok: boolean
  error?: string
}

export type Status = {
  uptime_seconds: number
  config_path: string
  last_reload: ReloadStatus
  providers_total: number
  providers_active: number
  models_serving: number
  providers: ProviderStatus[]
  models: ModelStatus[]
  skipped_routes: string[]
}

export type LogEntry = {
  seq: number
  line: string
}

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.status = status
    this.name = "ApiError"
  }
}

const TOKEN_KEY = "routerllm.admin.token"

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ""
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token.trim())
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/admin/api${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${getToken()}`,
      ...init?.headers,
    },
  })

  const text = await res.text()
  let payload: unknown = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = null
    }
  }

  if (!res.ok) {
    const message =
      typeof payload === "object" && payload !== null && "error" in payload
        ? String((payload as { error?: { message?: unknown } }).error?.message ?? "")
        : ""
    throw new ApiError(res.status, message || `request failed with ${res.status}`)
  }

  return payload as T
}

export const api = {
  status: (signal?: AbortSignal) => call<Status>("/status", { signal }),
  logs: (since: number, signal?: AbortSignal) => call<{ entries: LogEntry[] }>(`/logs?since=${since}`, { signal }),
  reload: () => call<Status>("/reload", { method: "POST" }),
  setProviderDisabled: (name: string, disabled: boolean) =>
    call<Status>(`/providers/${encodeURIComponent(name)}`, {
      method: "POST",
      body: JSON.stringify({ disabled }),
    }),
  setRouteDisabled: (modelId: string, index: number, disabled: boolean) =>
    call<Status>(`/routes/${encodeURIComponent(modelId)}/${index}`, {
      method: "POST",
      body: JSON.stringify({ disabled }),
    }),
  moveRoute: (modelId: string, index: number, direction: "up" | "down") =>
    call<Status>(`/routes/${encodeURIComponent(modelId)}/move`, {
      method: "POST",
      body: JSON.stringify({ index, direction }),
    }),
}
