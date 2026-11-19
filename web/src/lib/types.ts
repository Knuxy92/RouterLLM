// Wire shapes derived from the Go admin/telemetry structs.
// See internal/admin/status.go, internal/telemetry/{telemetry,metrics}.go,
// internal/admin/{config,pulse,admin}.go, internal/quota/quota.go.

export interface QuotaWindow {
  used_percent: number
  window_minutes: number
  reset_at: number
}

export interface QuotaSnapshot {
  primary?: QuotaWindow | null
  secondary?: QuotaWindow | null
  plan_type?: string
  updated_at: string
}

export interface KeyState {
  masked: string
  alive: boolean
  cooldown_left_seconds: number
  disabled: boolean
  quota?: QuotaSnapshot | null
}

export interface RouteLeg {
  provider: string
  model: string
  stylecall?: string | null
  sanitize_tool_names?: boolean
  dedupe_tools?: boolean
  clamp_tool_schemas?: boolean
  disabled: boolean
  active: boolean
  provider_disabled: boolean
  note?: string
}

export interface ProviderStatus {
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

export interface ModelStatus {
  model_id: string
  disabled: boolean
  serving: boolean
  chain: RouteLeg[]
}

export interface ReloadStatus {
  at: string
  ok: boolean
  error?: string
}

export interface StatusPayload {
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

export interface TelemetryAttempt {
  provider: string
  model?: string
  status: number
  latency_ms: number
  note?: string
  resp_body?: string
}

export interface LogEvent {
  seq: number
  time: string
  model: string
  request_id?: string
  status: number
  provider?: string
  upstream_model?: string
  key?: string
  ttft_ms: number
  duration_ms: number
  tokens_out: number
  err?: string
  resp_body?: string
  attempts?: TelemetryAttempt[]
}

export interface MetricsSummary {
  req: number
  err: number
  success_pct: number
  ttft_p50_ms: number
  ttft_p95_ms: number
  tokens: number
  uptime_pct: number
  tok_per_sec: number
}

export interface MetricsWindow {
  start: number
  req: number
  err: number
  tokens: number
  ttft_p50_ms?: number
  ttft_p95_ms?: number
}

export interface MetricsPayload {
  global: MetricsSummary
  global_weekly: MetricsSummary
  global_prev: MetricsSummary
  global_weekly_prev: MetricsSummary
  providers: Record<string, MetricsSummary>
  legs: Record<string, MetricsSummary>
  models: Record<string, MetricsSummary>
  hourly: MetricsWindow[]
  weekly: MetricsWindow[]
}

export interface PulsePayload {
  seq: number
  status_sig: string
  metrics_sig: string
}

export interface RequestsDelta {
  entries: LogEvent[]
  latest: number
}

export interface LogsPagePayload {
  entries: LogEvent[]
  page: number
  per_page: number
  total: number
  total_pages: number
  error_total: number
  latest: number
}

export interface UIConfig {
  poll_ms: number
  log_per_page: number
}

export interface TestConfig {
  prompt_bytes: number
  max_tokens: number
  default_tokens: number
  timeout_floor: number
  timeout_ceiling: number
  default_timeout: number
  default_prompt: string
}

export interface RuntimeConfig {
  ui: UIConfig
  test: TestConfig
  efforts: string[]
  stylecalls: string[]
  ring_capacity: number
}

export interface ModelCatalog {
  provider: string
  style: string
  source: string
  models: string[]
}

export interface AuthChallenge {
  challenge_id: string
  nonce: string
  expires_in: number
}

export interface AuthVerify {
  session: string
  expires_in: number
}

export interface TestProviderBody {
  model: string
  prompt: string
  max_tokens?: number
  effort?: string
  timeout_seconds?: number
}

export interface TestProviderResult {
  provider: string
  upstream_model: string
  style: string
  status: number
  ttft_ms: number
  duration_ms: number
  tokens: number
  content: string
  error: string
}

export interface LegInput {
  provider: string
  model: string
  effort?: string | null
  stylecall?: string | null
  disabled?: boolean
}

export interface AddLegBody {
  provider: string
  model: string
  reasoning_effort?: string
  stylecall?: string
  disabled: boolean
}

export interface LogFilters {
  provider: string
  model: string
  level: string
  q: string
  hours: number
  paused: boolean
}

export interface LogsPage {
  entries: LogEvent[]
  meta: {
    page: number
    per_page: number
    total: number
    total_pages: number
    error_total: number
    latest: number
  }
}

export interface StoreState {
  status: StatusPayload | null
  metrics: MetricsPayload | null
  config: RuntimeConfig
  requests: LogEvent[]
  recentFailures: LogEvent[]
  logs: LogsPage
  filters: LogFilters
  uptimeSeconds: number | null
}
