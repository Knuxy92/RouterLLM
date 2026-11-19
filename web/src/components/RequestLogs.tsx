import { useEffect, useState, useSyncExternalStore, type JSX } from "react"
import { api } from "../lib/api"
import { adaptLogs, fmtDur, fmtInt } from "../lib/data"
import { adminStore } from "../lib/store"
import type { LogEvent, LogFilters } from "../lib/types"
import Drawer from "./shared/Drawer"

const ALL_PROVIDERS = "All providers"
const ALL_MODELS = "All models"

const RANGE_HOURS: { label: string; hours: number }[] = [
  { label: "Last 1h", hours: 1 },
  { label: "Last 24h", hours: 24 },
  { label: "Last 7d", hours: 168 },
  { label: "Last 30d", hours: 720 },
]

const LEVELS = [
  { label: "All", value: "" },
  { label: "Info", value: "info" },
  { label: "Warn", value: "warn" },
  { label: "Error", value: "error" },
] as const

const LEVEL_BADGE: Record<string, string> = {
  info: "tone-info",
  warn: "tone-warn",
  error: "tone-error",
}

const CSV_HEADER = "time,level,message,provider,model,key,ttft,tok_s,status"
const FORMULA_LEAD = /^[=+@\t\r-]/

function refreshIcons() {
  const lucide = (window as unknown as { lucide?: { createIcons(): void } }).lucide
  lucide?.createIcons()
}

function csvQuote(v: unknown): string {
  const raw = String(v ?? "")
  const safe = FORMULA_LEAD.test(raw) ? "'" + raw : raw
  return '"' + safe.split('"').join('""') + '"'
}

function formatBody(s: string): string {
  try {
    const parsed: unknown = JSON.parse(s)
    return JSON.stringify(parsed, null, 2) ?? s
  } catch {
    return s
  }
}

interface FilterDropdownProps {
  options: string[]
  selected: string
  open: boolean
  onToggle: () => void
  onPick: (value: string) => void
}

function FilterDropdown({ options, selected, open, onToggle, onPick }: FilterDropdownProps) {
  return (
    <div className={`dd ${open ? "open" : ""}`}>
      <button type="button" className="dd-btn" onClick={onToggle}>
        <span className="dd-label">{selected}</span>
        <i data-lucide="chevron-down"></i>
      </button>
      <div className="dd-menu">
        {options.map((option) => (
          <button
            type="button"
            key={option}
            className={`dd-item${option === selected ? " active" : ""}`}
            onClick={() => onPick(option)}
          >
            {option}
          </button>
        ))}
      </div>
    </div>
  )
}

interface TraceBody {
  label: string
  body: string
}

function collectTraceBodies(event: LogEvent): TraceBody[] {
  const bodies: TraceBody[] = []
  for (const attempt of event.attempts || []) {
    if (attempt.resp_body) {
      const label = `${attempt.provider || "?"}${attempt.model ? "/" + attempt.model : ""}${attempt.status ? " · " + attempt.status : ""}`
      bodies.push({ label, body: attempt.resp_body })
    }
  }
  if (event.resp_body && !bodies.some((b) => b.body === event.resp_body)) {
    bodies.push({ label: `final → client${event.status ? " · " + event.status : ""}`, body: event.resp_body })
  }
  return bodies
}

function RequestTrace({ event }: { event: LogEvent }) {
  const adapted = adaptLogs([event])[0]
  const bodies = collectTraceBodies(event)
  const attempts = event.attempts?.length
    ? event.attempts
    : [{ provider: event.provider || "—", model: event.upstream_model || event.model, status: event.status, latency_ms: event.duration_ms }]
  const modelLabel = event.upstream_model && event.upstream_model !== event.model
    ? `${event.model} → ${event.upstream_model}`
    : event.model

  return (
    <>
      <p className="mb-1 text-xs text-muted-foreground">Request</p>
      <p className="mb-4 font-mono text-sm">{event.request_id || "req #" + event.seq}</p>

      <dl className="mb-4 grid grid-cols-2 gap-2.5 text-xs">
        <div className="rounded-md border bg-muted/30 p-2.5">
          <dt className="text-muted-foreground">Model</dt>
          <dd className="mt-1 font-mono">{modelLabel}</dd>
        </div>
        <div className="rounded-md border bg-muted/30 p-2.5">
          <dt className="text-muted-foreground">Provider / key</dt>
          <dd className="mt-1 font-mono">{event.provider || "—"} · {event.key || "—"}</dd>
        </div>
        <div className="rounded-md border bg-muted/30 p-2.5">
          <dt className="text-muted-foreground">Started</dt>
          <dd className="mt-1 font-mono">{new Date(event.time).toLocaleString()}</dd>
        </div>
        <div className="rounded-md border bg-muted/30 p-2.5">
          <dt className="text-muted-foreground">Duration</dt>
          <dd className="mt-1 font-mono">{fmtDur(event.duration_ms)}</dd>
        </div>
        <div className="rounded-md border bg-muted/30 p-2.5">
          <dt className="text-muted-foreground">TTFT</dt>
          <dd className="mt-1 font-mono">{event.ttft_ms ? fmtDur(event.ttft_ms) : "—"}</dd>
        </div>
        <div className="rounded-md border bg-muted/30 p-2.5">
          <dt className="text-muted-foreground">Tokens out</dt>
          <dd className="mt-1 font-mono">{event.tokens_out ? fmtInt(event.tokens_out) : "—"}</dd>
        </div>
      </dl>

      {event.err ? (
        <div className="mb-4 rounded-md border border-destructive/30 bg-destructive/5 p-3.5 text-xs">
          <p className="mb-1 flex items-center gap-2 font-semibold text-destructive">
            <i data-lucide="alert-triangle" className="size-3.5"></i>What happened
          </p>
          <p className="leading-relaxed text-foreground">{event.err}</p>
        </div>
      ) : (
        <div className="mb-4 rounded-md border bg-muted/30 p-3.5 text-xs">
          <p className="mb-1 font-semibold">Outcome</p>
          <p className="leading-relaxed text-foreground">{adapted.msg}</p>
        </div>
      )}

      {bodies.length > 0 && (
        <div className="mb-4">
          <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Upstream response</p>
          {bodies.map((item, index) => (
            <div key={index} className="mb-2 rounded-md border bg-muted/30 p-3">
              <p className="mb-1.5 font-mono text-[11px] font-medium text-muted-foreground">{item.label}</p>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] leading-relaxed text-foreground">{formatBody(item.body)}</pre>
            </div>
          ))}
        </div>
      )}

      <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Attempt timeline</p>
      <ol className="mb-4 flex flex-col gap-0 border-l pl-4 text-xs">
        {attempts.map((attempt, index) => (
          <li key={index} className="relative py-1.5">
            <span className={`absolute -left-[21px] top-3 size-2 rounded-full ${attempt.status && attempt.status < 400 ? "bg-primary" : "bg-amber-500"}`}></span>
            <p className="font-mono">
              attempt {index + 1}/{attempts.length} → {attempt.provider}{attempt.model ? "/" + attempt.model : ""} ·{" "}
              <span className={attempt.status && attempt.status < 400 ? "" : "text-destructive font-semibold"}>{attempt.status || "—"}</span> · {fmtDur(attempt.latency_ms)}
              {attempt.note ? " · " + attempt.note : ""}
            </p>
          </li>
        ))}
      </ol>

      <p className="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground/70">Raw event</p>
      <pre className="overflow-x-auto whitespace-pre-wrap rounded-md border bg-muted/30 p-3 font-mono text-[11px] leading-relaxed text-muted-foreground">{JSON.stringify(event, null, 2)}</pre>
    </>
  )
}

export default function RequestLogs(): JSX.Element {
  const state = useSyncExternalStore(adminStore.subscribe, adminStore.getSnapshot, adminStore.getSnapshot)
  const { entries, meta } = state.logs
  const filters = state.filters
  const providers = state.status?.providers || []
  const models = state.status?.models || []

  const [searchText, setSearchText] = useState(filters.q)
  const [openDropdown, setOpenDropdown] = useState<string | null>(null)
  const [selected, setSelected] = useState<LogEvent | null>(null)
  const [exporting, setExporting] = useState(false)

  useEffect(() => {
    refreshIcons()
  })

  useEffect(() => {
    const onDocClick = (event: MouseEvent) => {
      const target = event.target as Element | null
      if (!target?.closest?.(".dd")) setOpenDropdown(null)
    }
    document.addEventListener("click", onDocClick)
    return () => document.removeEventListener("click", onDocClick)
  }, [])

  useEffect(() => {
    const timer = setTimeout(() => {
      if (searchText.trim() !== filters.q) void adminStore.setLogFilter({ q: searchText.trim() })
    }, 250)
    return () => clearTimeout(timer)
  }, [searchText, filters.q])

  useEffect(() => {
    const status = state.status
    if (!status) return
    const providerNames = (status.providers || []).map((p) => p.name)
    const modelNames = (status.models || []).map((m) => m.model_id)
    const stale: Partial<LogFilters> = {}
    if (filters.provider && !providerNames.includes(filters.provider)) stale.provider = ""
    if (filters.model && !modelNames.includes(filters.model)) stale.model = ""
    if (Object.keys(stale).length) void adminStore.setLogFilter(stale)
  }, [state.status, filters.provider, filters.model])

  const rows = adaptLogs(entries)
  const currentLevel = filters.level || "All"
  const currentRange = RANGE_HOURS.find((r) => r.hours === (filters.hours || 24))?.label ?? "Last 24h"
  const errorTotal = meta.error_total ?? 0
  const paused = filters.paused

  function toggleDropdown(id: string) {
    setOpenDropdown((current) => (current === id ? null : id))
  }

  function goToLogPage(page: number) {
    const totalPages = Math.max(1, meta.total_pages)
    if (page < 1 || page > totalPages) return
    void adminStore.loadLogsPage(page)
  }

  function renderPagination(): JSX.Element[] {
    const buttons: JSX.Element[] = []
    const page = meta.page
    const totalPages = Math.max(1, meta.total_pages)
    const first = Math.max(1, Math.min(page - 2, totalPages - 4))
    const last = Math.min(totalPages, first + 4)
    let key = 0

    const push = (label: string | number, target: number, opts: { active?: boolean; disabled?: boolean; ellipsis?: boolean } = {}) => {
      if (opts.ellipsis) {
        buttons.push(<span key={key++} className="px-1 text-xs text-muted-foreground">…</span>)
        return
      }
      buttons.push(
        <button
          key={key++}
          data-log-page={target}
          disabled={opts.disabled}
          onClick={() => goToLogPage(target)}
          className={`rounded border px-2 py-1 hover:bg-accent disabled:opacity-40${opts.active ? " bg-primary font-medium text-primary-foreground" : ""}`}
        >
          {label}
        </button>,
      )
    }

    push("Prev", page - 1, { disabled: page === 1 })
    if (first > 1) {
      push(1, 1)
      if (first > 2) push("…", 0, { ellipsis: true })
    }
    for (let n = first; n <= last; n++) push(n, n, { active: n === page })
    if (last < totalPages) {
      if (last < totalPages - 1) push("…", 0, { ellipsis: true })
      push(totalPages, totalPages)
    }
    push("Next", page + 1, { disabled: page === totalPages })
    return buttons
  }

  async function exportCsv() {
    setExporting(true)
    try {
      const res = await api.requestsPage({
        page: 1,
        perPage: state.config.ring_capacity,
        provider: filters.provider,
        model: filters.model,
        level: filters.level,
        q: filters.q,
        hours: filters.hours,
      })
      const lines = adaptLogs(res.entries || []).map((r) =>
        [r.time, r.level, r.msg, r.provider, r.model, r.key, r.ttft, r.tps ?? "", r.status ?? ""]
          .map(csvQuote)
          .join(","),
      )
      const blob = new Blob([CSV_HEADER + "\n" + lines.join("\n")], { type: "text/csv;charset=utf-8" })
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement("a")
      anchor.href = url
      anchor.download = "routerllm-requests.csv"
      document.body.appendChild(anchor)
      anchor.click()
      anchor.remove()
      setTimeout(() => URL.revokeObjectURL(url), 0)
      setExporting(false)
    } catch {
      setTimeout(() => setExporting(false), 1500)
    }
  }

  const start = (meta.page - 1) * meta.per_page

  return (
    <section id="page-logs">
      <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Request logs</h1>
          <p className="text-sm text-muted-foreground">
            Every routed request with full trace. Click a row to inspect.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            id="log-export"
            type="button"
            disabled={exporting}
            onClick={() => void exportCsv()}
            className="inline-flex items-center gap-2 rounded-md border bg-background px-3 py-1.5 text-sm shadow-sm hover:bg-accent disabled:opacity-50"
          >
            <i data-lucide="download" className="size-3.5"></i>Export CSV
          </button>
        </div>
      </div>

      <div className="mb-3 flex flex-wrap items-center gap-2 rounded-lg border bg-card p-3 shadow-sm">
        <div className="relative min-w-[220px] flex-1">
          <i data-lucide="search" className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"></i>
          <input
            id="log-search"
            name="search"
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            placeholder="Search message, x-request-id, key…"
            className="w-full rounded-md border bg-transparent py-1.5 pl-8 pr-3 text-sm outline-none focus:border-ring focus:ring-2 focus:ring-ring/30"
          />
        </div>
        <FilterDropdown
          options={[ALL_PROVIDERS, ...providers.map((p) => p.name)]}
          selected={filters.provider || ALL_PROVIDERS}
          open={openDropdown === "provider"}
          onToggle={() => toggleDropdown("provider")}
          onPick={(value) => void adminStore.setLogFilter({ provider: value === ALL_PROVIDERS ? "" : value })}
        />
        <FilterDropdown
          options={[ALL_MODELS, ...models.map((m) => m.model_id)]}
          selected={filters.model || ALL_MODELS}
          open={openDropdown === "model"}
          onToggle={() => toggleDropdown("model")}
          onPick={(value) => void adminStore.setLogFilter({ model: value === ALL_MODELS ? "" : value })}
        />
        <div className="seg">
          {LEVELS.map((level) => (
            <button
              key={level.label}
              type="button"
              className={`seg-btn${currentLevel === level.label ? " active" : ""}`}
              onClick={() => void adminStore.setLogFilter({ level: level.value })}
            >
              {level.label}
              {level.value === "error" && (
                <span id="log-error-count" className="ml-1 rounded-full bg-destructive/15 px-1.5 text-[10px] font-semibold text-destructive">{errorTotal}</span>
              )}
            </button>
          ))}
        </div>
        <FilterDropdown
          options={RANGE_HOURS.map((r) => r.label)}
          selected={currentRange}
          open={openDropdown === "range"}
          onToggle={() => toggleDropdown("range")}
          onPick={(value) => void adminStore.setLogFilter({ hours: RANGE_HOURS.find((r) => r.label === value)?.hours ?? 24 })}
        />
        <button
          id="log-pause"
          type="button"
          onClick={() => void adminStore.setLogFilter({ paused: !paused })}
          className="inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-sm text-muted-foreground hover:bg-accent"
        >
          <i data-lucide={paused ? "play" : "pause"} className="size-3.5"></i>{paused ? "Resume" : "Pause"}
        </button>
      </div>

      <div className="overflow-hidden rounded-lg border bg-card shadow-sm">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[980px] text-left text-sm">
            <thead>
              <tr className="border-b text-xs uppercase tracking-wide text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">Time</th>
                <th className="px-3 py-2.5 font-medium">Level</th>
                <th className="px-3 py-2.5 font-medium">Message</th>
                <th className="px-3 py-2.5 font-medium">Provider</th>
                <th className="px-3 py-2.5 font-medium">Model</th>
                <th className="px-3 py-2.5 font-medium">Key</th>
                <th className="px-3 py-2.5 text-right font-medium">TTFT</th>
                <th className="px-3 py-2.5 text-right font-medium">Tok/s</th>
                <th className="px-3 py-2.5 text-right font-medium">Status</th>
                <th className="px-3 py-2.5"></th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {rows.length === 0 ? (
                <tr>
                  <td colSpan={10} className="px-4 py-10 text-center text-sm text-muted-foreground">No requests match the current filters.</td>
                </tr>
              ) : (
                rows.map((row) => (
                  <tr
                    key={row.seq}
                    className={`${row.status && row.status !== 200 ? "log-error-row" : ""} cursor-pointer hover:bg-muted/50`}
                    onClick={() => setSelected(row.entry)}
                  >
                    <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs">{row.time}</td>
                    <td className="px-3 py-2.5"><span className={`badge ${LEVEL_BADGE[row.level] ?? "tone-info"}`}>{row.level}</span></td>
                    <td className="max-w-[340px] truncate px-3 py-2.5">{row.msg}</td>
                    <td className="px-3 py-2.5 font-mono text-xs">{row.provider}</td>
                    <td className="px-3 py-2.5 font-mono text-xs">{row.model}</td>
                    <td className="px-3 py-2.5 font-mono text-xs">{row.key}</td>
                    <td className="px-3 py-2.5 text-right font-mono text-xs">{row.ttft}</td>
                    <td className={`px-3 py-2.5 text-right font-mono text-xs${row.tps == null ? " text-muted-foreground" : ""}`}>{row.tps ?? "—"}</td>
                    <td className="px-3 py-2.5 text-right">
                      {row.status == null ? (
                        <span className="text-muted-foreground">—</span>
                      ) : (
                        <span className={`badge font-mono ${row.status === 200 ? "tone-ok" : "tone-error"}`}>{row.status}</span>
                      )}
                    </td>
                    <td className="px-3 py-2.5 text-right"><i data-lucide="chevron-right" className="ml-auto size-4 text-muted-foreground"></i></td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
        <div className="flex items-center justify-between border-t px-4 py-2.5 text-xs text-muted-foreground">
          <span>
            Rows{" "}
            <span className="font-mono text-foreground">{meta.total ? `${start + 1}–${start + rows.length}` : "0"}</span>{" "}
            of <span className="font-mono text-foreground">{meta.total || 0}</span>
          </span>
          <span className="inline-flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <span className={`dot ${paused ? "dot-warn" : "dot-live"}`}></span>
            {paused ? "paused" : `auto-refresh ${state.config.ui.poll_ms / 1000}s`}
          </span>
          <div className="flex items-center gap-1">{renderPagination()}</div>
        </div>
      </div>

      <Drawer
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={
          <>
            Request trace{" "}
            {selected && (
              selected.status ? (
                <span className={`badge font-mono ${selected.status < 400 ? "tone-ok" : "tone-error"}`}>{selected.status}</span>
              ) : (
                <span className="badge tone-error">no response</span>
              )
            )}
          </>
        }
      >
        {selected && <RequestTrace event={selected} />}
      </Drawer>
    </section>
  )
}
