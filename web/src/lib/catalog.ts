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

export interface OpenCodeProvider {
  models?: Record<string, OpenCodeModel>
}

export type OpenCodeCatalog = Record<string, OpenCodeModel | OpenCodeProvider>

function isProvider(value: OpenCodeModel | OpenCodeProvider): value is OpenCodeProvider {
  return Object.prototype.hasOwnProperty.call(value, "models")
}

export function parseOpenCodeCatalog(payload: OpenCodeCatalog | null | undefined): OpenCodeModel[] {
  if (!payload) return []

  const best = new Map<string, OpenCodeModel>()
  const consider = (model: OpenCodeModel) => {
    if (!model.id) return
    const key = normalize(model.id)
    const previous = best.get(key)
    const score = (candidate: OpenCodeModel) =>
      (candidate.limit?.context ? 2 : 0) +
      (candidate.limit?.output ? 2 : 0) +
      (candidate.reasoning_options?.length ?? 0) +
      (candidate.description ? 1 : 0) +
      (candidate.canonical_model_id && normalize(candidate.canonical_model_id) === normalize(candidate.id) ? 3 : 0)
    if (!previous || score(model) > score(previous)) best.set(key, model)
  }

  for (const value of Object.values(payload)) {
    if (isProvider(value)) {
      for (const model of Object.values(value.models ?? {})) consider(model)
      continue
    }
    consider(value)
  }

  return [...best.values()]
}

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
  free: boolean
}

const EMPTY = "—"

function normalize(value: string): string {
  return value.trim().toLowerCase()
}

function tail(value: string): string {
  const slash = value.lastIndexOf("/")
  return normalize(slash >= 0 ? value.slice(slash + 1) : value)
}

export function buildOpenCodeIndex(models: OpenCodeModel[]): Map<string, OpenCodeModel> {
  const index = new Map<string, OpenCodeModel>()

  for (const model of models) {
    if (!model.id) continue
    if (!index.has(normalize(model.id))) index.set(normalize(model.id), model)
    if (model.canonical_model_id && !index.has(normalize(model.canonical_model_id))) {
      index.set(normalize(model.canonical_model_id), model)
    }
  }

  return index
}

function stripQualitySuffix(value: string): string {
  const colon = value.lastIndexOf(":")
  return colon > 0 ? value.slice(0, colon) : value
}

function suffixVariants(value: string): string[] {
  const out = [value]
  const base = stripQualitySuffix(value)
  if (base !== value) out.push(base)
  for (let current = base; ;) {
    const stripped = current.replace(/-(free|max|thinking|flash)$/i, "")
    if (stripped === current) break
    current = stripped
    if (!out.includes(current)) out.push(current)
  }
  return out
}

export function resolveMetadata(nativeId: string, index: Map<string, OpenCodeModel>): OpenCodeModel | null {
  if (!nativeId) return null

  const variants = suffixVariants(nativeId)
  for (const variant of variants) {
    const hit = index.get(normalize(variant))
    if (hit) return hit
  }

  const wantedTails = new Set(variants.flatMap((variant) => [tail(variant), normalize(variant)]))
  const candidates = new Map<string, OpenCodeModel>()
  for (const model of index.values()) {
    const modelTail = tail(stripQualitySuffix(model.id))
    if (wantedTails.has(modelTail) || [...wantedTails].some((want) => want.length >= 6 && modelTail.endsWith(want))) {
      candidates.set(model.id, model)
    }
  }
  if (candidates.size === 0) return null

  const rank = (model: OpenCodeModel): number => {
    let score = 0
    if (!model.id.includes(":")) score += 8
    for (const variant of variants) {
      if (normalize(variant) === normalize(model.id)) score += 6
      if (tail(variant) === tail(model.id)) score += 4
    }
    if (model.canonical_model_id && normalize(model.canonical_model_id) === normalize(model.id)) score += 2
    if (model.reasoning_options?.length) score += 1
    if (model.limit?.context) score += 1
    score -= Math.min(model.id.length, 60) / 1000
    return score
  }

  const ranked = [...candidates.values()].sort((a, b) => rank(b) - rank(a))
  if (ranked.length === 1 || rank(ranked[0]) > rank(ranked[1])) return ranked[0]

  return null
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
  // The catalog prices per token; a model is free when neither side charges.
  const cost = model?.cost
  const free = Boolean(cost && cost.input === 0 && cost.output === 0)

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
    free,
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
  }

  return rows
}

export function enrichOpenCodeCatalog(models: OpenCodeModel[]): ModelRow[] {
  return models
    .filter((model) => Boolean(model.id))
    .map((model) => toModelRow(model.id, "opencode", model))
}

export interface OpenCodeCatalogEntry {
  provider: string
  model: OpenCodeModel
}

// parseOpenCodeCatalogEntries keeps provider provenance: one entry per
// (provider, model) pair, with none of parseOpenCodeCatalog's cross-provider
// dedupe. This is what feeds the per-provider sidebar on the models page.
export function parseOpenCodeCatalogEntries(payload: OpenCodeCatalog | null | undefined): OpenCodeCatalogEntry[] {
  if (!payload) return []

  const entries: OpenCodeCatalogEntry[] = []
  for (const [provider, value] of Object.entries(payload)) {
    if (isProvider(value)) {
      for (const model of Object.values(value.models ?? {})) {
        if (model.id) entries.push({ provider, model })
      }
      continue
    }
    if (value.id) entries.push({ provider, model: value })
  }

  return entries
}

export function enrichOpenCodeCatalogByProvider(entries: OpenCodeCatalogEntry[]): ModelRow[] {
  return entries
    .filter((entry) => Boolean(entry.model.id))
    .map((entry) => toModelRow(entry.model.id, entry.provider, entry.model))
}
