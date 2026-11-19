import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type JSX } from "react"
import { api } from "../lib/api"
import {
  parseOpenCodeCatalog,
  buildOpenCodeIndex,
  enrichFromStatus,
  enrichOpenCodeCatalog,
  enrichRows,
  type ModelRow,
  type OpenCodeCatalog,
  type OpenCodeModel,
} from "../lib/catalog"
import { fmtCompact } from "../lib/data"
import type { ModelCatalog, ProviderStatus } from "../lib/types"
import { adminStore } from "../lib/store"
import { RefreshCwIcon } from "./icons"
import FilterDropdown from "./shared/FilterDropdown"

const OPENCODE_CATALOG_URL = "https://models.opencode.ai/api.json"
const EMPTY_ROWS: ModelRow[] = []

const REASONING_FILTERS = [
  { label: "All reasoning", value: "all" },
  { label: "Effort", value: "effort" },
  { label: "Toggle", value: "toggle" },
  { label: "No metadata", value: "none" },
]
const ALL_SOURCES = "All sources"
const PAGE_SIZES = [10, 20, 50, 100]

type Tab = { key: string; label: string }

function ReasoningCell({ row }: { row: ModelRow }): JSX.Element {
  if (row.reasoning === "—") return <span className="text-muted-foreground">—</span>
  return (
    <span className="whitespace-nowrap">
      <span className={`badge ${row.reasoning === "effort" ? "tone-ok" : "tone-warn"}`}>{row.reasoning}</span>
      {row.reasoningValues.length > 0 && (
        <span className="ml-1.5 font-mono text-[11px] text-muted-foreground">
          {row.reasoningValues.join("·")}
        </span>
      )}
    </span>
  )
}

function NumberCell({ value }: { value: number | null }): JSX.Element {
  return <span className="font-mono text-xs">{fmtCompact(value)}</span>
}

export default function ModelsDetail(): JSX.Element {
  const subscribe = useCallback((listener: () => void) => adminStore.subscribe(listener), [])
  const getSnapshot = useCallback(() => adminStore.getSnapshot(), [])
  const state = useSyncExternalStore(subscribe, getSnapshot, getSnapshot)

  const [catalogModels, setCatalogModels] = useState<OpenCodeModel[]>([])
  const [providerCatalogs, setProviderCatalogs] = useState<Record<string, ModelCatalog>>({})
  const [loading, setLoading] = useState(true)
  const [activeTab, setActiveTab] = useState("configured")
  const [query, setQuery] = useState("")
  const [reasoningFilter, setReasoningFilter] = useState("all")
  const [sourceFilter, setSourceFilter] = useState("all")
  const [openDropdown, setOpenDropdown] = useState<string | null>(null)
  const [page, setPage] = useState(1)
  const [perPage, setPerPage] = useState(10)
  const loadedOnce = useRef(false)

  useEffect(() => {
    const onDocClick = (event: MouseEvent) => {
      const target = event.target as Element | null
      if (!target?.closest?.(".dd")) setOpenDropdown(null)
    }
    document.addEventListener("click", onDocClick)
    return () => document.removeEventListener("click", onDocClick)
  }, [])

  function toggleDropdown(id: string) {
    setOpenDropdown((current) => (current === id ? null : id))
  }

  function resetPage() {
    setPage(1)
  }

  function selectTab(key: string) {
    setActiveTab(key)
    resetPage()
  }

  const load = useCallback(async () => {
    setLoading(true)

    try {
      const response = await fetch(OPENCODE_CATALOG_URL)
      if (!response.ok) throw new Error(`status ${response.status}`)
      const payload = (await response.json()) as OpenCodeCatalog
      setCatalogModels(parseOpenCodeCatalog(payload))
    } catch {
      setCatalogModels([])
    }

    const catalogStyles = new Set(["codex", "cline", "workbuddy", "opencode", "alysis"])
    const providers = (adminStore.getStatus()?.providers ?? []).filter(
      (provider: ProviderStatus) =>
        !provider.disabled && provider.keys_alive > 0 && catalogStyles.has(provider.style),
    )
    const results = await Promise.all(
      providers.map(async (provider) => {
        try {
          return { name: provider.name, data: await api.providerModels(provider.name) }
        } catch {
          return { name: provider.name, data: null }
        }
      }),
    )

    const loaded: Record<string, ModelCatalog> = {}
    for (const result of results) {
      if (result.data) loaded[result.name] = result.data
    }
    setProviderCatalogs(loaded)
    setLoading(false)
  }, [])

  useEffect(() => {
    if (!loadedOnce.current && state.status) {
      loadedOnce.current = true
      void load()
    }
  }, [state.status, load])

  const index = useMemo(() => buildOpenCodeIndex(catalogModels), [catalogModels])

  const tabs = useMemo<Tab[]>(() => {
    const list: Tab[] = [
      { key: "configured", label: "Configured" },
      { key: "opencode", label: "OpenCode" },
    ]
    for (const name of Object.keys(providerCatalogs)) {
      if (name === "opencode") continue
      if (!list.some((tab) => tab.key === name)) list.push({ key: name, label: name })
    }
    return list
  }, [providerCatalogs])

  const rowsByTab = useMemo<Record<string, ModelRow[]>>(() => {
    const result: Record<string, ModelRow[]> = {
      configured: enrichFromStatus(state.status ?? null, index),
      opencode: enrichOpenCodeCatalog(catalogModels),
    }
    for (const [name, data] of Object.entries(providerCatalogs)) {
      result[name] = enrichRows(data.models, data.source || name, index)
    }
    return result
  }, [state.status, index, catalogModels, providerCatalogs])

  const rows = rowsByTab[activeTab] ?? EMPTY_ROWS
  const q = query.trim().toLowerCase()
  const visible = useMemo(() => {
    return rows.filter((row) => {
      if (q && !`${row.id} ${row.displayName} ${row.description} ${row.source}`.toLowerCase().includes(q)) return false
      if (reasoningFilter === "none" && row.reasoning !== "—") return false
      if (reasoningFilter !== "all" && reasoningFilter !== "none" && row.reasoning !== reasoningFilter) return false
      if (sourceFilter !== "all" && row.source !== sourceFilter) return false
      return true
    })
  }, [rows, q, reasoningFilter, sourceFilter])

  const sources = useMemo(() => [...new Set(rows.map((row) => row.source))].sort(), [rows])

  const totalPages = Math.max(1, Math.ceil(visible.length / perPage))
  const currentPage = Math.min(page, totalPages)
  const start = (currentPage - 1) * perPage
  const pageRows = visible.slice(start, start + perPage)

  function goToPage(target: number) {
    if (target < 1 || target > totalPages) return
    setPage(target)
  }

  function renderPagination(): JSX.Element[] {
    const buttons: JSX.Element[] = []
    const first = Math.max(1, Math.min(currentPage - 2, totalPages - 4))
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
          type="button"
          disabled={opts.disabled}
          onClick={() => goToPage(target)}
          className={`rounded border px-2 py-1 hover:bg-accent disabled:opacity-40${opts.active ? " bg-primary font-medium text-primary-foreground" : ""}`}
        >
          {label}
        </button>,
      )
    }

    push("Prev", currentPage - 1, { disabled: currentPage === 1 })
    if (first > 1) {
      push(1, 1)
      if (first > 2) push("…", 0, { ellipsis: true })
    }
    for (let n = first; n <= last; n++) push(n, n, { active: n === currentPage })
    if (last < totalPages) {
      if (last < totalPages - 1) push("…", 0, { ellipsis: true })
      push(totalPages, totalPages)
    }
    push("Next", currentPage + 1, { disabled: currentPage === totalPages })
    return buttons
  }

  return (
    <section>
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Models detail</h1>
          <p className="mt-1 text-sm text-muted-foreground">Catalog of models routed through this proxy</p>
        </div>
        <button
          type="button"
          onClick={() => void load()}
          disabled={loading}
          className="inline-flex items-center gap-2 rounded-md border bg-card px-3 py-2 text-xs font-medium shadow-sm transition-colors hover:bg-muted/60 disabled:opacity-50"
        >
          <RefreshCwIcon className="size-3.5" />
          {loading ? "Loading…" : "Refresh catalog"}
        </button>
      </div>

      <div className="flex flex-wrap gap-1 border-b border-border">
        {tabs.map((tab) => {
          const count = rowsByTab[tab.key]?.length ?? 0
          return (
            <button
              key={tab.key}
              type="button"
              onClick={() => selectTab(tab.key)}
              className={`border-b-2 px-3.5 py-2.5 text-xs transition-colors ${
                activeTab === tab.key
                  ? "border-foreground font-semibold text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
            >
              {tab.label} <span className="ml-1 font-mono text-[11px] text-muted-foreground">{count}</span>
            </button>
          )
        })}
      </div>

      <div className="overflow-hidden rounded-b-lg border border-t-0 bg-card shadow-sm">
        <div className="flex flex-wrap gap-2 p-4">
          <input
            value={query}
            onChange={(event) => {
              setQuery(event.target.value)
              resetPage()
            }}
            placeholder="Search model id, name, family…"
            aria-label="Search models"
            className="min-w-[200px] flex-1 rounded-md border bg-background px-3 py-2 text-xs"
          />
          <FilterDropdown
            options={REASONING_FILTERS.map((filter) => filter.label)}
            selected={REASONING_FILTERS.find((filter) => filter.value === reasoningFilter)?.label ?? "All reasoning"}
            open={openDropdown === "reasoning"}
            onToggle={() => toggleDropdown("reasoning")}
            onPick={(label) => {
              setReasoningFilter(REASONING_FILTERS.find((filter) => filter.label === label)?.value ?? "all")
              resetPage()
              setOpenDropdown(null)
            }}
          />
          <FilterDropdown
            options={[ALL_SOURCES, ...sources]}
            selected={sourceFilter === "all" ? ALL_SOURCES : sourceFilter}
            open={openDropdown === "source"}
            onToggle={() => toggleDropdown("source")}
            onPick={(label) => {
              setSourceFilter(label === ALL_SOURCES ? "all" : label)
              resetPage()
              setOpenDropdown(null)
            }}
          />
          <FilterDropdown
            options={PAGE_SIZES.map((size) => String(size))}
            selected={String(perPage)}
            open={openDropdown === "perPage"}
            onToggle={() => toggleDropdown("perPage")}
            onPick={(label) => {
              setPerPage(Number(label))
              resetPage()
              setOpenDropdown(null)
            }}
          />
        </div>

        <div className="overflow-x-auto">
          <table className="w-full min-w-[820px] text-left text-xs">
            <thead>
              <tr className="border-b bg-muted/40 text-[11px] uppercase tracking-wide text-muted-foreground">
                <th className="px-3 py-2.5 font-medium">Model Id</th>
                <th className="px-3 py-2.5 font-medium">Reasoning</th>
                <th className="px-3 py-2.5 text-right font-medium">Context</th>
                <th className="px-3 py-2.5 text-right font-medium">Max output</th>
                <th className="px-3 py-2.5 font-medium">Modalities</th>
                <th className="px-3 py-2.5 font-medium">Source</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {pageRows.map((row, index) => (
                <tr key={`${row.source}-${row.id}-${index}`} className="hover:bg-muted/30">
                  <td className="px-3 py-2.5">
                    <div className="font-mono font-medium">{row.id}</div>
                    {row.description && (
                      <div className="max-w-[320px] truncate text-[11px] text-muted-foreground" title={row.description}>
                        {row.description}
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-2.5"><ReasoningCell row={row} /></td>
                  <td className="px-3 py-2.5 text-right"><NumberCell value={row.context} /></td>
                  <td className="px-3 py-2.5 text-right"><NumberCell value={row.maxOutput} /></td>
                  <td className="px-3 py-2.5">
                    {row.modalities.length > 0 ? (
                      <span className="flex flex-wrap gap-1">
                        {row.modalities.map((modality) => (
                          <span key={modality} className="badge tone-info">{modality}</span>
                        ))}
                      </span>
                    ) : <span className="text-muted-foreground">—</span>}
                  </td>
                  <td className="px-3 py-2.5"><span className="badge tone-info">{row.source}</span></td>
                </tr>
              ))}
              {pageRows.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-3 py-8 text-center text-xs text-muted-foreground">
                    {loading ? "Loading catalog…" : "No models match these filters."}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <div className="flex items-center justify-between border-t px-4 py-2.5 text-[11px] text-muted-foreground">
          <span>
            Rows{" "}
            <span className="font-mono text-foreground">{visible.length ? `${start + 1}–${start + pageRows.length}` : "0"}</span>{" "}
            of <span className="font-mono text-foreground">{visible.length}</span> · values marked — are not provided by that source
          </span>
          <div className="flex items-center gap-1">{renderPagination()}</div>
        </div>
      </div>
    </section>
  )
}
