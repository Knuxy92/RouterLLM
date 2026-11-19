import type { StatusPayload } from "./types"

export interface OpenCodeReasoningOption {
  type: string
  values?: string[]
}

export interface OpenCodeModel {
  id: string
  name?: string
  description?: string
  family?: string
  reasoning?: boolean
  reasoning_options?: OpenCodeReasoningOption[]
  modalities?: { input?: string[]; output?: string[] }
  limit?: { context?: number; output?: number }
  canonical_model_id?: string
  open_weights?: boolean
  cost?: Record<string, number>
}

export type OpenCodeCatalog = Record<string, OpenCodeModel>

export interface ModelRow {
  id: string
  displayName: string
  description: string
  reasoning: string
  reasoningValues: string[]
  context: number | null
  maxOutput: number | null
  modalities: string[]
  source: string
}

const EMPTY = "—"

function normalize(value: string): string {
  return value.trim().toLowerCase()
}

function tail(value: string): string {
  const slash = value.lastIndexOf("/")
  return normalize(slash >= 0 ? value.slice(slash + 1) : value)
}

export function buildOpenCodeIndex(catalog: OpenCodeCatalog): Map<string, OpenCodeModel> {
  const index = new Map<string, OpenCodeModel>()
  const models = Object.values(catalog)

  for (const model of models) {
    if (!model.id) continue
    if (!index.has(normalize(model.id))) index.set(normalize(model.id), model)
    if (model.canonical_model_id && !index.has(normalize(model.canonical_model_id))) {
      index.set(normalize(model.canonical_model_id), model)
    }
  }

  return index
}

export function resolveMetadata(nativeId: string, index: Map<string, OpenCodeModel>): OpenCodeModel | null {
  if (!nativeId) return null

  const exact = index.get(normalize(nativeId))
  if (exact) return exact

  const wantedTail = tail(nativeId)
  let found: OpenCodeModel | null = null
  for (const [key, model] of index) {
    if (key.includes("/")) continue
    if (tail(key) !== wantedTail) continue
    if (found && found.id !== model.id) return null
    found = model
  }

  return found
}

function reasoningOf(model: OpenCodeModel | null): { label: string; values: string[] } {
  if (!model) return { label: EMPTY, values: [] }
  const options = model.reasoning_options ?? []
  const effort = options.find((option) => option.type === "effort")
  if (effort) return { label: "effort", values: effort.values ?? [] }
  if (options.some((option) => option.type === "toggle")) return { label: "toggle", values: [] }
  if (model.reasoning === true) return { label: "enabled", values: [] }
  return { label: EMPTY, values: [] }
}

export function toModelRow(nativeId: string, source: string, model: OpenCodeModel | null): ModelRow {
  const reasoning = reasoningOf(model)
  const modalities = model?.modalities
    ? [...(model.modalities.input ?? []), ...(model.modalities.output ?? [])].filter(
        (value, index, all) => value && all.indexOf(value) === index,
      )
    : []

  return {
    id: nativeId,
    displayName: model?.name ?? nativeId,
    description: model?.description ?? "",
    reasoning: reasoning.label,
    reasoningValues: reasoning.values,
    context: model?.limit?.context ?? null,
    maxOutput: model?.limit?.output ?? null,
    modalities,
    source,
  }
}

export function enrichRows(nativeIds: string[] | null | undefined, source: string, index: Map<string, OpenCodeModel>): ModelRow[] {
  return (nativeIds ?? []).map((id) => toModelRow(id, source, resolveMetadata(id, index)))
}

export function enrichFromStatus(status: StatusPayload | null, index: Map<string, OpenCodeModel>): ModelRow[] {
  if (!status) return []

  const rows: ModelRow[] = []
  const seen = new Set<string>()

  const add = (id: string, source: string) => {
    const key = `${source}\u0000${id}`
    if (!id || seen.has(key)) return
    seen.add(key)
    rows.push(toModelRow(id, source, resolveMetadata(id, index)))
  }

  for (const model of status.models ?? []) {
    if (model.disabled) continue
    const chain = model.chain ?? []
    const leg = chain.find((entry) => !entry.disabled && !entry.provider_disabled) ?? chain[0]
    if (!leg) continue
    add(model.model_id, leg.provider)
    add(leg.model, leg.provider)
  }

  return rows
}

export function enrichOpenCodeCatalog(catalog: OpenCodeCatalog): ModelRow[] {
  return Object.values(catalog)
    .filter((model) => Boolean(model.id))
    .map((model) => toModelRow(model.id, "opencode", model))
}
