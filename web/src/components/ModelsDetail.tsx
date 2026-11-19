import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type JSX } from "react"
import { api } from "../lib/api"
import {
  buildOpenCodeIndex,
  enrichFromStatus,
  enrichOpenCodeCatalog,
  enrichRows,
  type ModelRow,
  type OpenCodeCatalog,
} from "../lib/catalog"
import { fmtCompact } from "../lib/data"
import type { ModelCatalog, ProviderStatus } from "../lib/types"
import { adminStore } from "../lib/store"
import { RefreshCwIcon } from "./icons"

const OPENCODE_CATALOG_URL = "https://models.opencode.ai/api.json"
const EMPTY_INDEX = new Map()
const EMPTY_ROWS: ModelRow[] = []

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

  const [catalog, setCatalog] = useState<OpenCodeCatalog | null>(null)
  const [providerCatalogs, setProviderCatalogs] = useState<Record<string, ModelCatalog>>({})
  const [loading, setLoading] = useState(true)
  const [activeTab, setActiveTab] = useState("configured")
  const [query, setQuery] = useState("")
  const [reasoningFilter, setReasoningFilter] = useState("all")
  const [sourceFilter, setSourceFilter] = useState("all")
  const loadedOnce = useRef(false)

  const load = useCallback(async () => {
    setLoading(true)

    try {
      const response = await fetch(OPENCODE_CATALOG_URL)
      if (!response.ok) throw new Error(`status ${response.status}`)
      const payload = (await response.json()) as OpenCodeCatalog
      setCatalog(payload)
    } catch {
      setCatalog(null)
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

  const index = useMemo(() => (catalog ? buildOpenCodeIndex(catalog) : EMPTY_INDEX), [catalog])

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
      opencode: catalog ? enrichOpenCodeCatalog(catalog) : [],
    }
    for (const [name, data] of Object.entries(providerCatalogs)) {
      result[name] = enrichRows(data.models, data.source || name, index)
    }
    return result
  }, [state.status, index, catalog, providerCatalogs])

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
              onClick={() => setActiveTab(tab.key)}
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
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search model id, name, family…"
            aria-label="Search models"
            className="min-w-[200px] flex-1 rounded-md border bg-background px-3 py-2 text-xs"
          />
          <select
            value={reasoningFilter}
            onChange={(event) => setReasoningFilter(event.target.value)}
            aria-label="Filter by reasoning"
            className="rounded-md border bg-background px-2.5 py-2 text-xs"
          >
            <option value="all">All reasoning</option>
            <option value="effort">Effort</option>
            <option value="toggle">Toggle</option>
            <option value="none">No metadata</option>
          </select>
          <select
            value={sourceFilter}
            onChange={(event) => setSourceFilter(event.target.value)}
            aria-label="Filter by source"
            className="rounded-md border bg-background px-2.5 py-2 text-xs"
          >
            <option value="all">All sources</option>
            {sources.map((source) => (
              <option key={source} value={source}>{source}</option>
            ))}
          </select>
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
              {visible.map((row, index) => (
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
              {visible.length === 0 && (
                <tr>
                  <td colSpan={6} className="px-3 py-8 text-center text-xs text-muted-foreground">
                    {loading ? "Loading catalog…" : "No models match these filters."}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        <div className="border-t px-4 py-2.5 text-[11px] text-muted-foreground">
          Showing {visible.length} of {rows.length} · values marked — are not provided by that source
        </div>
      </div>
    </section>
  )
}
