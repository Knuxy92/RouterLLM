import { useCallback, useState, useSyncExternalStore, type JSX } from "react"
import { api } from "../lib/api"
import {
  KEY_TONE,
  NOTE_TONE,
  STATUS_META,
  fmtDur,
  fmtInt,
  providerAgg,
  providerModelRows,
  type AdaptedModel,
  type AdaptedProvider,
} from "../lib/data"
import { adminStore } from "../lib/store"
import type { QuotaSnapshot, TestProviderBody, TestProviderResult } from "../lib/types"
import Dialog from "./shared/Dialog"
import Drawer from "./shared/Drawer"
import {
  ArrowDownIcon,
  ArrowUpIcon,
  BarChart3Icon,
  ChevronUpIcon,
  FlaskConicalIcon,
  PlayIcon,
  PlusIcon,
  XIcon,
} from "./icons"

function errorDisplay(err: string): JSX.Element {
  const destructive = err.includes("text-destructive")
  const text = err.replace(/<[^>]*>/g, "")
  return destructive ? <span className="text-destructive">{text}</span> : <>{text}</>
}

function keyLine(p: AdaptedProvider): string {
  if (!p.keyList?.length) return p.keys
  const on = p.keyList.filter((k) => k.on).length
  if (on === 0) return `0/${p.keyList.length} · all disabled`
  const bad = p.keyList.filter((k) => k.on && k.status !== "healthy")
  return `${on}/${p.keyList.length} · ${bad.length ? bad.length + " " + bad[0].status : "all healthy"}`
}

function fmtResetIn(resetAt: number): string {
  if (!resetAt) return ""
  const secs = resetAt - Math.floor(Date.now() / 1000)
  if (secs <= 0) return "resetting"
  if (secs >= 86400) return `${Math.round(secs / 86400)}d`
  if (secs >= 3600) return `${Math.round(secs / 3600)}h`
  return `${Math.max(1, Math.round(secs / 60))}m`
}

function quotaTone(pct: number): string {
  if (pct >= 80) return "bg-destructive"
  if (pct >= 60) return "bg-amber-500"
  return "bg-primary"
}

function quotaCell(quota: QuotaSnapshot | null | undefined): JSX.Element | null {
  const win = quota?.primary
  if (!win) return null

  const reset = fmtResetIn(win.reset_at)
  const hours = win.window_minutes ? Math.round(win.window_minutes / 60) : null
  const title = [
    quota?.plan_type ? `plan ${quota.plan_type}` : "",
    hours ? `window ${hours}h` : "",
    reset ? `resets in ${reset}` : "",
  ]
    .filter(Boolean)
    .join(" · ")

  return (
    <span className="inline-flex items-center gap-1.5" title={title || undefined}>
      <span className="inline-block h-1.5 w-14 overflow-hidden rounded-full bg-muted align-middle">
        <span
          className={`block h-1.5 rounded-full ${quotaTone(win.used_percent)}`}
          style={{ width: `${Math.min(100, win.used_percent)}%` }}
        ></span>
      </span>
      <span className="font-mono">{win.used_percent}%</span>
      {reset && <span className="text-muted-foreground">reset {reset}</span>}
    </span>
  )
}

function clamp(value: string, lo: number, hi: number, fallback: number): number {
  const n = Number(value)
  if (!Number.isFinite(n)) return fallback
  return Math.min(hi, Math.max(lo, Math.round(n)))
}

type TestResult =
  | { kind: "error"; message: string }
  | { kind: "ok"; result: TestProviderResult }

function TestResultView({ result, fallbackModel }: { result: TestResult; fallbackModel: string }): JSX.Element {
  if (result.kind === "error") {
    return (
      <div className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-xs">
        <p className="mb-1 font-semibold text-destructive">Test failed</p>
        <p className="leading-relaxed">{result.message}</p>
      </div>
    )
  }

  const res = result.result
  const ok = res.status === 200 && !res.error
  const tps = res.duration_ms > 0 ? Math.round(res.tokens / (res.duration_ms / 1000)) : null
  const chip = (label: string, value: JSX.Element | string): JSX.Element => (
    <div className="rounded-md border bg-muted/30 px-2 py-1.5">
      <p className="text-[10px] uppercase tracking-wide text-muted-foreground">{label}</p>
      <p className="mt-0.5 truncate font-mono text-xs font-medium">{value}</p>
    </div>
  )

  return (
    <>
      <div className="grid grid-cols-3 gap-2 text-xs">
        {chip("Status", <span className={`badge ${ok ? "tone-ok" : "tone-error"}`}>{res.status ?? "—"}</span>)}
        {chip("TTFT", res.ttft_ms > 0 ? fmtDur(res.ttft_ms) : res.ttft_ms === 0 ? "0 ms" : "—")}
        {chip("Duration", fmtDur(res.duration_ms))}
        {chip("Tok/s", tps == null ? "—" : fmtInt(tps))}
        {chip("Tokens", fmtInt(res.tokens))}
        {chip("Upstream model", <span className="font-mono">{res.upstream_model || fallbackModel}</span>)}
      </div>
      {res.error && (
        <div className="mt-2 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-xs">
          <p className="leading-relaxed">{res.error}</p>
        </div>
      )}
      {res.content && (
        <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/30 p-3 font-mono text-[11px] leading-relaxed">
          {res.content}
        </pre>
      )}
    </>
  )
}

export default function ProvidersModels(): JSX.Element {
  const subscribe = useCallback((listener: () => void) => adminStore.subscribe(listener), [])
  const getSnapshot = useCallback(() => adminStore.getSnapshot(), [])
  useSyncExternalStore(subscribe, getSnapshot, getSnapshot)

  const [drawerProvider, setDrawerProvider] = useState<string | null>(null)
  const [analyticsOpen, setAnalyticsOpen] = useState(false)

  const [testOpen, setTestOpen] = useState(false)
  const [testModel, setTestModel] = useState("")
  const [testEffort, setTestEffort] = useState("")
  const [testStylecall, setTestStylecall] = useState("")
  const [testPrompt, setTestPrompt] = useState("")
  const [testMaxTokens, setTestMaxTokens] = useState("256")
  const [testTimeout, setTestTimeout] = useState("20")
  const [testRunning, setTestRunning] = useState(false)
  const [testResult, setTestResult] = useState<TestResult | null>(null)

  const [legDialogModel, setLegDialogModel] = useState<string | null>(null)
  const [legProvider, setLegProvider] = useState("")
  const [legUpstream, setLegUpstream] = useState("")
  const [legEffort, setLegEffort] = useState("")
  const [legStylecall, setLegStylecall] = useState("")
  const [legDisabled, setLegDisabled] = useState(false)

  const view = adminStore.getView()
  const config = adminStore.getConfig()
  const status = adminStore.getStatus()
  const metrics = adminStore.getMetrics()
  const agg = providerAgg(metrics)

  function openProvider(name: string): void {
    const rows = providerModelRows(name, metrics, status)
    setDrawerProvider(name)
    setAnalyticsOpen(false)
    setTestOpen(false)
    setTestModel(rows[0]?.modelId ?? "")
    setTestEffort("")
    setTestStylecall("")
    setTestPrompt(config.test.default_prompt)
    setTestMaxTokens(String(config.test.default_tokens))
    setTestTimeout(String(config.test.default_timeout))
    setTestResult(null)
    setTestRunning(false)
  }

  function openLegDialog(model: string): void {
    const modelRow = view.models.find((m) => m.name === model)
    const existing = new Set(modelRow?.legs.map((leg) => leg.route.split("/")[0]) ?? [])
    const candidates = view.providers.filter((p) => p.on && !existing.has(p.name)).map((p) => p.name)

    setLegDialogModel(model)
    setLegProvider(candidates[0] ?? "")
    setLegUpstream("")
    setLegEffort("")
    setLegStylecall("")
    setLegDisabled(false)
  }

  function confirmLeg(): void {
    if (!legDialogModel || !legProvider) return
    void adminStore.addLeg(legDialogModel, {
      provider: legProvider,
      model: legUpstream.trim(),
      effort: legEffort || undefined,
      stylecall: legStylecall || undefined,
      disabled: legDisabled,
    })
    setLegDialogModel(null)
  }

  async function runTest(name: string): Promise<void> {
    const caps = config.test
    const body: TestProviderBody & { stylecall?: string } = {
      model: testModel,
      prompt: testPrompt,
      max_tokens: clamp(testMaxTokens, 1, caps.max_tokens, caps.default_tokens),
      effort: testEffort,
      timeout_seconds: clamp(testTimeout, caps.timeout_floor, caps.timeout_ceiling, caps.default_timeout),
    }
    if (testStylecall) body.stylecall = testStylecall

    setTestRunning(true)
    setTestResult(null)
    try {
      const result = await api.testProvider(name, body)
      setTestResult({ kind: "ok", result })
    } catch (err) {
      setTestResult({ kind: "error", message: err instanceof Error ? err.message : String(err) })
    } finally {
      setTestRunning(false)
    }
  }

  function providerCard(p: AdaptedProvider): JSX.Element {
    const s = STATUS_META[p.status]
    const p50 = agg[p.name]
    return (
      <div
        key={p.name}
        data-provider={p.name}
        className={`cursor-pointer rounded-lg border bg-card p-4 shadow-sm transition-all hover:shadow-md${p.on ? "" : " opacity-60"}`}
        onClick={() => openProvider(p.name)}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className={`dot ${s.dot}`}></span>
            <p className="text-sm font-semibold">{p.name}</p>
            <span className={`badge ${s.badge}`}>{s.label}</span>
          </div>
          <input
            type="checkbox"
            className="sw"
            checked={p.on}
            onClick={(e) => e.stopPropagation()}
            onChange={(e) => void adminStore.toggleProvider(p.name, !e.target.checked)}
          />
        </div>
        <dl className="mt-3 flex flex-col gap-1.5 text-xs">
          <div className="flex justify-between">
            <dt className="text-muted-foreground">Keys</dt>
            <dd className="font-mono">{keyLine(p)}</dd>
          </div>
          <div className="flex justify-between">
            <dt className="text-muted-foreground">Requests 24h</dt>
            <dd className="font-mono">{p.req}</dd>
          </div>
          <div className="flex justify-between">
            <dt className="text-muted-foreground">
              <span
                className="has-tip"
                data-tip="Median time to first token, weighted by request count across this provider's models · click the card for the per-model breakdown"
              >
                TTFT p50 · weighted
              </span>
            </dt>
            <dd className="font-mono">{p50 ? fmtDur(p50) : "—"}</dd>
          </div>
          <div className="flex justify-between">
            <dt className="text-muted-foreground">Errors 24h</dt>
            <dd className="font-mono">{errorDisplay(p.err)}</dd>
          </div>
          <div className="flex justify-between">
            <dt className="text-muted-foreground">Uptime 24h</dt>
            <dd className="font-mono">{p.up}</dd>
          </div>
        </dl>
      </div>
    )
  }

  function modelBlock(m: AdaptedModel): JSX.Element {
    return (
      <div
        key={m.name}
        data-model-block={m.name}
        className={`rounded-lg border bg-card shadow-sm transition-opacity${m.on ? "" : " opacity-60"}`}
      >
        <div className="flex flex-wrap items-center gap-3 border-b px-5 py-3">
          <span className="font-mono text-sm font-medium">{m.name}</span>
          <span className="ml-auto flex items-center gap-4 text-xs text-muted-foreground">
            <span>
              req <span className="font-mono text-foreground">{m.req}</span>
            </span>
            <span>
              success <span className="font-mono text-foreground">{m.ok}</span>
            </span>
            <span>
              ttft <span className="font-mono text-foreground">{fmtDur(m.ttft)}</span>
            </span>
            <input
              type="checkbox"
              className="sw"
              checked={m.on}
              onChange={(e) => void adminStore.toggleModel(m.name, !e.target.checked)}
            />
          </span>
        </div>
        <ul className="divide-y px-5 py-1 text-sm">
          {m.legs.map((leg, i) => (
            <li
              key={`${leg.route}:${i}`}
              className={`flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2.5 transition-opacity${leg.on === false ? " opacity-50" : ""}`}
            >
              <span className="badge tone-info font-mono">{i + 1}</span>
              <span className="min-w-0 truncate font-mono text-xs">{leg.route}</span>
              <span className={`badge ${NOTE_TONE[leg.tone]}`}>{leg.note}</span>
              {leg.stylecall && <span className="badge tone-info font-mono">{leg.stylecall}</span>}
              <span className="ml-auto flex items-center gap-1.5">
                <button
                  type="button"
                  disabled={i === 0}
                  onClick={() => void adminStore.moveLeg(m.name, i, -1)}
                  className="rounded border p-1 text-muted-foreground hover:bg-accent disabled:opacity-30"
                >
                  <ArrowUpIcon className="size-3" />
                </button>
                <button
                  type="button"
                  disabled={i === m.legs.length - 1}
                  onClick={() => void adminStore.moveLeg(m.name, i, 1)}
                  className="rounded border p-1 text-muted-foreground hover:bg-accent disabled:opacity-30"
                >
                  <ArrowDownIcon className="size-3" />
                </button>
                <button
                  type="button"
                  disabled={m.legs.length <= 1}
                  onClick={() => void adminStore.removeLeg(m.name, i)}
                  className="rounded border p-1 text-muted-foreground hover:bg-accent hover:text-destructive disabled:opacity-30"
                >
                  <XIcon className="size-3" />
                </button>
                <input
                  type="checkbox"
                  className="sw"
                  checked={leg.on !== false}
                  onChange={(e) => void adminStore.toggleLeg(m.name, i, !e.target.checked)}
                />
              </span>
            </li>
          ))}
          <li className="py-2.5">
            <button
              type="button"
              onClick={() => openLegDialog(m.name)}
              className="flex w-full items-center justify-center gap-2 rounded-md border border-dashed py-2 text-xs text-muted-foreground hover:bg-accent"
            >
              <PlusIcon className="size-3" />Add failover leg
            </button>
          </li>
        </ul>
      </div>
    )
  }

  const drawerP = drawerProvider ? view.providers.find((p) => p.name === drawerProvider) : undefined
  const drawerS = drawerP ? STATUS_META[drawerP.status] : undefined
  const rows = drawerProvider ? providerModelRows(drawerProvider, metrics, status) : []
  const totalReq = rows.reduce((sum, r) => sum + r.req, 0)
  const maxP50 = Math.max(...rows.map((r) => r.p50), 1)
  const slowestP50 = Math.max(...rows.map((r) => r.p50), 0)
  const aggP50 = drawerProvider ? agg[drawerProvider] : undefined
  const providerSummary = metrics?.providers?.[drawerProvider ?? ""]
  const quotaWins = drawerP?.keyList.map((k) => k.quota?.primary).filter(Boolean) ?? []
  const worstQuota = quotaWins.length ? Math.max(...quotaWins.map((q) => q!.used_percent)) : null

  const legModelRow = legDialogModel ? view.models.find((m) => m.name === legDialogModel) : undefined
  const existingLegs = new Set(legModelRow?.legs.map((leg) => leg.route.split("/")[0]) ?? [])
  const legCandidates = view.providers.filter((p) => p.on && !existingLegs.has(p.name)).map((p) => p.name)

  return (
    <>
      <div className="mb-5">
        <h1 className="text-2xl font-semibold tracking-tight">Providers &amp; Models</h1>
        <p className="text-sm text-muted-foreground">
          Toggle providers, manage key pools and edit fallback chains per model. Click a provider card
          to see TTFT broken down by model.
        </p>
      </div>

      <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Providers</p>
      <div id="provider-grid" className="mb-6 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {view.providers.length ? (
          view.providers.map(providerCard)
        ) : (
          <p className="col-span-full rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">
            No providers configured.
          </p>
        )}
      </div>

      <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">
        Models &amp; fallback chains
      </p>
      <div id="model-list" className="flex flex-col gap-3">
        {view.models.length ? (
          view.models.map(modelBlock)
        ) : (
          <p className="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">
            No models configured.
          </p>
        )}
      </div>

      <Drawer
        open={drawerProvider !== null}
        onClose={() => setDrawerProvider(null)}
        title={
          drawerP && drawerS ? (
            <>
              <span className={`dot ${drawerS.dot}`}></span>
              <span className="font-semibold">{drawerP.name}</span>
              <span className={`badge ${drawerS.badge}`}>{drawerS.label}</span>
            </>
          ) : null
        }
      >
        {drawerP && (
          <>
            <dl className="mb-5 grid grid-cols-3 gap-3 text-xs">
              <div className="rounded-md border bg-muted/30 p-3">
                <dt className="text-muted-foreground">
                  <span className="has-tip tip-below" data-tip="Median time to first token, weighted by request count across this provider's models">
                    TTFT p50 · weighted
                  </span>
                </dt>
                <dd className="mt-1.5 font-mono text-base font-semibold">{aggP50 ? fmtDur(aggP50) : "—"}</dd>
              </div>
              <div className="rounded-md border bg-muted/30 p-3">
                <dt className="text-muted-foreground">
                  <span className="has-tip tip-below" data-tip="Total routed requests in the last 24 hours">Requests 24h</span>
                </dt>
                <dd className="mt-1.5 font-mono text-base font-semibold">{drawerP.req}</dd>
              </div>
              <div className="rounded-md border bg-muted/30 p-3">
                <dt className="text-muted-foreground">
                  <span className="has-tip tip-below" data-tip="Upstream failures in the last 24 hours">Errors 24h</span>
                </dt>
                <dd className="mt-1.5 font-mono text-base font-semibold">{errorDisplay(drawerP.err)}</dd>
              </div>
              <div className="rounded-md border bg-muted/30 p-3">
                <dt className="text-muted-foreground">
                  <span className="has-tip tip-below" data-tip="API keys enabled out of configured total">Keys</span>
                </dt>
                <dd className="mt-1.5 font-mono">{keyLine(drawerP)}</dd>
              </div>
              <div className="rounded-md border bg-muted/30 p-3">
                <dt className="text-muted-foreground">
                  <span className="has-tip tip-below" data-tip="Share of 5-minute buckets with at least one success">Uptime 24h</span>
                </dt>
                <dd className="mt-1.5 font-mono">{drawerP.up}</dd>
              </div>
              <div className="rounded-md border bg-muted/30 p-3">
                <dt className="text-muted-foreground">
                  <span className="has-tip tip-below" data-tip="Distinct upstream models routed through this provider">Models served</span>
                </dt>
                <dd className="mt-1.5 font-mono text-base font-semibold">{drawerP.model_count ?? rows.length}</dd>
              </div>
            </dl>

            <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">TTFT by model</p>
            {rows.length === 0 ? (
              <div className="rounded-md border border-dashed p-6 text-center text-xs text-muted-foreground">
                No models are currently routed through this provider.
              </div>
            ) : (
              <>
                <div className="overflow-x-auto rounded-md border">
                  <table className="w-full min-w-[480px] text-left text-xs">
                    <thead>
                      <tr className="border-b bg-muted/40 text-[11px] uppercase tracking-wide text-muted-foreground">
                        <th className="px-4 py-2.5 font-medium">Model</th>
                        <th className="px-4 py-2.5 text-right font-medium">
                          <span className="has-tip tip-below" data-tip="Requests through this model in the last 24 hours, with share of provider total">Requests</span>
                        </th>
                        <th className="px-4 py-2.5 font-medium">
                          <span className="has-tip tip-below tip-left" data-tip="Median time to first token for this model">TTFT p50</span>
                        </th>
                        <th className="px-4 py-2.5 text-right font-medium">
                          <span className="has-tip tip-below" data-tip="95th percentile time to first token — the slow tail">p95</span>
                        </th>
                        <th className="px-4 py-2.5 text-right font-medium">
                          <span className="has-tip tip-below tip-right" data-tip="Output tokens per second">Tok/s</span>
                        </th>
                      </tr>
                    </thead>
                    <tbody className="divide-y">
                      {rows.map((r) => {
                        const slowest = rows.length > 1 && r.p50 === slowestP50
                        return (
                          <tr key={r.route}>
                            <td className="px-4 py-2.5 font-mono">
                              {r.modelId}
                              <div className="mt-1.5 h-1.5 w-full max-w-28 rounded-full bg-muted">
                                <div
                                  className={`h-1.5 rounded-full ${slowest ? "bg-amber-500" : "bg-primary"}`}
                                  style={{ width: `${Math.round((r.p50 / maxP50) * 100)}%` }}
                                ></div>
                              </div>
                            </td>
                            <td className="px-4 py-2.5 text-right font-mono">
                              {fmtInt(r.req)}
                              <span className="block text-[10px] text-muted-foreground">
                                {totalReq ? Math.round((r.req / totalReq) * 100) : 0}% share
                              </span>
                            </td>
                            <td className="px-4 py-2.5 font-mono">
                              {r.p50 ? (
                                <span
                                  className={`${slowest ? "font-semibold text-amber-500" : ""} has-tip tip-below`}
                                  data-tip={slowest ? "Slowest TTFT p50 in this provider · bar is relative to this value" : "Median time to first token"}
                                >
                                  {fmtDur(r.p50)}
                                </span>
                              ) : (
                                "—"
                              )}
                            </td>
                            <td className="px-4 py-2.5 text-right font-mono">{r.p95 ? fmtDur(r.p95) : "—"}</td>
                            <td className="px-4 py-2.5 text-right font-mono">{r.tps || "—"}</td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
                <p className="mt-2 text-[11px] leading-relaxed text-muted-foreground">
                  Aggregate p50 is request-weighted across the models above — heavy models pull the provider average.
                  Bar = relative to the slowest model.
                </p>
              </>
            )}

            <div className="mb-2 mt-4 flex items-center justify-between">
              <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Manage keys</p>
              {worstQuota != null && (
                <span className={`badge ${worstQuota >= 80 ? "tone-error" : worstQuota >= 60 ? "tone-warn" : "tone-ok"}`}>
                  quota worst {worstQuota}% · {quotaWins.length}/{drawerP.keyList.length} keys
                </span>
              )}
            </div>
            <div className="overflow-hidden rounded-md border">
              {drawerP.keyList.map((k, i) => (
                <div
                  key={k.id}
                  className={`flex items-center gap-3 px-3 py-2.5 text-xs${i > 0 ? " border-t" : ""}${k.on ? "" : " opacity-50"}`}
                >
                  <span className="font-mono">{k.masked}</span>
                  <span className={`badge ${KEY_TONE[k.status]}`}>{k.status}</span>
                  {quotaCell(k.quota)}
                  <span className="ml-auto text-muted-foreground">
                    {k.status === "cooldown" ? (
                      <>
                        cooldown <span className="font-mono text-foreground">{k.cooldown}s</span>
                      </>
                    ) : k.status === "disabled" ? (
                      "manually disabled"
                    ) : null}
                  </span>
                  <input
                    type="checkbox"
                    className="sw"
                    checked={k.on}
                    onChange={(e) => void adminStore.toggleKey(drawerP.name, i, !e.target.checked)}
                  />
                </div>
              ))}
            </div>
            <p className="mt-1.5 text-[11px] text-muted-foreground">
              Keys arrive masked from the backend. The manual toggle is runtime-only — a process restart re-enables
              disabled keys.
            </p>

            {rows.length > 0 && (
              <>
                <div className="mt-4 flex items-center justify-between border-t pt-3">
                  <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Test model</p>
                  <button
                    type="button"
                    className="inline-flex items-center gap-2 rounded-md border bg-background px-3 py-1.5 text-xs shadow-sm hover:bg-accent"
                    onClick={() => {
                      setTestOpen(!testOpen)
                      if (!testOpen) setTestResult(null)
                    }}
                  >
                    {testOpen ? (
                      <>
                        <ChevronUpIcon className="size-3" />Close test
                      </>
                    ) : (
                      <>
                        <FlaskConicalIcon className="size-3" />Open test
                      </>
                    )}
                  </button>
                </div>
                {testOpen && (
                  <div className="mt-3">
                    <div className="rounded-md border bg-card p-4">
                      <div className="grid grid-cols-2 gap-2.5 text-xs">
                        <div className="col-span-2 flex flex-col gap-1">
                          <span className="text-muted-foreground">Upstream model</span>
                          <select
                            value={testModel}
                            onChange={(e) => setTestModel(e.target.value)}
                            className="rounded-md border bg-background px-2 py-1 text-xs"
                          >
                            {rows.map((r) => (
                              <option key={r.modelId} value={r.modelId}>
                                {r.modelId}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className="flex flex-col gap-1">
                          <span className="text-muted-foreground">Effort</span>
                          <select
                            value={testEffort}
                            onChange={(e) => setTestEffort(e.target.value)}
                            className="rounded-md border bg-background px-2 py-1 text-xs"
                          >
                            <option value="">(none)</option>
                            {config.efforts.map((e) => (
                              <option key={e} value={e}>
                                {e}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className="flex flex-col gap-1">
                          <span className="text-muted-foreground">Call style</span>
                          <select
                            value={testStylecall}
                            onChange={(e) => setTestStylecall(e.target.value)}
                            className="rounded-md border bg-background px-2 py-1 text-xs"
                          >
                            <option value="">(auto)</option>
                            {config.stylecalls.map((s) => (
                              <option key={s} value={s}>
                                {s}
                              </option>
                            ))}
                          </select>
                        </div>
                        <div className="col-span-2 flex flex-col gap-1">
                          <span className="text-muted-foreground">Prompt</span>
                          <textarea
                            rows={2}
                            value={testPrompt}
                            onChange={(e) => setTestPrompt(e.target.value)}
                            className="resize-y rounded-md border bg-background px-2 py-1 text-xs"
                          ></textarea>
                        </div>
                        <div className="flex flex-col gap-1">
                          <span className="text-muted-foreground">Max tokens</span>
                          <input
                            type="number"
                            min={1}
                            max={config.test.max_tokens}
                            value={testMaxTokens}
                            onChange={(e) => setTestMaxTokens(e.target.value)}
                            className="rounded-md border bg-background px-2 py-1 text-xs"
                          />
                        </div>
                        <div className="flex flex-col gap-1">
                          <span className="text-muted-foreground">Timeout (s)</span>
                          <input
                            type="number"
                            min={config.test.timeout_floor}
                            max={config.test.timeout_ceiling}
                            value={testTimeout}
                            onChange={(e) => setTestTimeout(e.target.value)}
                            className="rounded-md border bg-background px-2 py-1 text-xs"
                          />
                        </div>
                      </div>
                      <button
                        type="button"
                        disabled={testRunning}
                        onClick={() => void runTest(drawerP.name)}
                        className="mt-3 inline-flex items-center gap-2 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground shadow-sm hover:bg-primary/90 disabled:opacity-50"
                      >
                        <PlayIcon className="size-3.5" />
                        {testRunning ? "Testing…" : "Run test"}
                      </button>
                      {testResult && (
                        <div className="mt-3">
                          <TestResultView result={testResult} fallbackModel={testModel} />
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </>
            )}

            <div className="mt-4 flex items-center justify-between border-t pt-3">
              <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Analytics</p>
              <button
                type="button"
                className="inline-flex items-center gap-2 rounded-md border bg-background px-3 py-1.5 text-xs shadow-sm hover:bg-accent"
                onClick={() => setAnalyticsOpen(!analyticsOpen)}
              >
                {analyticsOpen ? (
                  <>
                    <ChevronUpIcon className="size-3" />Hide analytics
                  </>
                ) : (
                  <>
                    <BarChart3Icon className="size-3" />View analytics
                  </>
                )}
              </button>
            </div>
            {analyticsOpen && (
              <div className="mt-3">
                {!providerSummary || providerSummary.req === 0 ? (
                  <p className="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">
                    No metrics recorded for this provider in the last 24 hours.
                  </p>
                ) : (
                  <div className="rounded-lg border bg-card p-4">
                    <p className="mb-2 text-sm font-medium">Last 24 hours</p>
                    <dl className="grid grid-cols-3 gap-2.5 text-xs">
                      {[
                        ["Requests", fmtInt(providerSummary.req)],
                        ["Errors", fmtInt(providerSummary.err)],
                        ["Success", providerSummary.req > 0 ? providerSummary.success_pct + "%" : "—"],
                        ["TTFT p50", providerSummary.ttft_p50_ms ? fmtDur(providerSummary.ttft_p50_ms) : "—"],
                        ["TTFT p95", providerSummary.ttft_p95_ms ? fmtDur(providerSummary.ttft_p95_ms) : "—"],
                        ["Tok/s", providerSummary.tok_per_sec ? fmtInt(Math.round(providerSummary.tok_per_sec)) : "—"],
                      ].map(([label, value]) => (
                        <div key={label} className="rounded-md border bg-muted/30 p-2.5">
                          <dt className="text-muted-foreground">{label}</dt>
                          <dd className="mt-1 font-mono text-lg font-semibold">{value}</dd>
                        </div>
                      ))}
                    </dl>
                  </div>
                )}
              </div>
            )}
          </>
        )}
      </Drawer>

      <Dialog
        open={legDialogModel !== null}
        onClose={() => setLegDialogModel(null)}
        title={<span>Add failover leg</span>}
      >
        <p className="mt-1 text-xs text-muted-foreground">
          Fallback route for <span className="font-mono text-foreground">{legDialogModel}</span>
        </p>
        <div className="mt-4">
          <p className="text-xs font-medium text-muted-foreground">Provider</p>
          <select
            value={legProvider}
            disabled={legCandidates.length === 0}
            onChange={(e) => setLegProvider(e.target.value)}
            className="mt-1.5 w-full rounded-md border bg-transparent px-2.5 py-2 text-sm text-foreground font-mono"
          >
            {legCandidates.length ? (
              legCandidates.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))
            ) : (
              <option value="">— every healthy provider is already in this chain —</option>
            )}
          </select>
        </div>
        <div className="mt-3">
          <label htmlFor="leg-upstream" className="block text-xs font-medium text-muted-foreground">
            Upstream model
          </label>
          <input
            id="leg-upstream"
            type="text"
            autoComplete="off"
            spellCheck={false}
            placeholder="defaults to the router model name"
            value={legUpstream}
            onChange={(e) => setLegUpstream(e.target.value)}
            className="mt-1.5 w-full rounded-md border bg-transparent px-2.5 py-2 text-sm font-mono text-foreground focus:border-ring focus:outline-none"
          />
        </div>
        <div className="mt-3">
          <p className="text-xs font-medium text-muted-foreground">Reasoning effort</p>
          <select
            value={legEffort}
            onChange={(e) => setLegEffort(e.target.value)}
            className="mt-1.5 w-full rounded-md border bg-transparent px-2.5 py-2 text-sm text-foreground font-mono"
          >
            <option value="">(none)</option>
            {config.efforts.map((e) => (
              <option key={e} value={e}>
                {e}
              </option>
            ))}
          </select>
        </div>
        <div className="mt-3">
          <p className="text-xs font-medium text-muted-foreground">Call style</p>
          <select
            value={legStylecall}
            onChange={(e) => setLegStylecall(e.target.value)}
            className="mt-1.5 w-full rounded-md border bg-transparent px-2.5 py-2 text-sm text-foreground font-mono"
          >
            <option value="">(auto)</option>
            {config.stylecalls.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </div>
        <label className="mt-3 flex items-center gap-2 text-xs text-muted-foreground">
          <input type="checkbox" className="sw" checked={legDisabled} onChange={(e) => setLegDisabled(e.target.checked)} />
          Start disabled
        </label>
        <p className="mt-3 text-[11px] text-muted-foreground">
          The leg is appended last (lowest priority) and starts in standby. Reorder it with the arrows afterwards.
        </p>
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            onClick={() => setLegDialogModel(null)}
            className="rounded-md border px-3 py-1.5 text-sm hover:bg-accent"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={legCandidates.length === 0}
            onClick={confirmLeg}
            className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-40"
          >
            Add leg
          </button>
        </div>
      </Dialog>
    </>
  )
}
