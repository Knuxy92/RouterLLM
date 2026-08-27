import { ArrowDown, ArrowUp } from "lucide-react"

import type { ModelStatus, RouteLeg } from "@/lib/api"
import { BorderBeam } from "@/components/magic/border-beam"
import { Badge, Button, EmptyState, Ident } from "@/components/ui/primitives"
import { Switch } from "@/components/ui/switch"
import { cn } from "@/lib/utils"

type RouteChainProps = {
  model: ModelStatus
  pending: boolean
  controlsDisabled: boolean
  onToggleLeg: (index: number, disabled: boolean) => void
  onMoveLeg: (index: number, direction: "up" | "down") => void
}

export function RouteChain({
  model,
  pending,
  controlsDisabled,
  onToggleLeg,
  onMoveLeg,
}: RouteChainProps) {
  return (
    <article className="border-b border-line px-4 py-4 last:border-b-0">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <h3 className="ident text-sm font-semibold text-ink">{model.model_id}</h3>
        {model.serving ? (
          <Badge tone="live">serving</Badge>
        ) : (
          <Badge tone="dead">404 — no live leg</Badge>
        )}
      </div>

      {model.chain.length === 0 ? (
        <EmptyState title="No route legs configured" detail={`Add a provider route for ${model.model_id}.`} />
      ) : (
        <ol className="flex flex-col gap-2 lg:grid lg:grid-cols-2 lg:gap-3 xl:grid-cols-3 2xl:grid-cols-4">
          {model.chain.map((leg, index) => (
            <RouteStep
              key={routeKey(model.chain, index)}
              leg={leg}
              index={index}
              last={index === model.chain.length - 1}
              pending={pending}
              controlsDisabled={controlsDisabled}
              modelId={model.model_id}
              chainLength={model.chain.length}
              onToggle={(disabled) => onToggleLeg(index, disabled)}
              onMove={(direction) => onMoveLeg(index, direction)}
            />
          ))}
        </ol>
      )}
    </article>
  )
}

type RouteStepProps = {
  leg: RouteLeg
  index: number
  last: boolean
  pending: boolean
  controlsDisabled: boolean
  modelId: string
  chainLength: number
  onToggle: (disabled: boolean) => void
  onMove: (direction: "up" | "down") => void
}

function RouteStep({
  leg,
  index,
  last,
  pending,
  controlsDisabled,
  modelId,
  chainLength,
  onToggle,
  onMove,
}: RouteStepProps) {
  const disabled = leg.disabled || leg.provider_disabled

  return (
    <li className="flex min-w-0 flex-col">
      <div
        className={cn(
          "relative flex min-w-0 flex-1 flex-col gap-2 rounded-lg border bg-inset px-3 py-2.5",
          leg.active && "border-live/45",
          !leg.active && !disabled && "border-line",
          disabled && "border-parked/30 opacity-60",
          pending && "opacity-70",
        )}
      >
        {leg.active && <BorderBeam duration={6} size={70} />}

        <div className="flex min-w-0 items-start gap-2">
          <span className={cn("ident shrink-0 text-sm tabular-nums", leg.active ? "text-live" : "text-ink-dim")}>
            {String(index + 1).padStart(2, "0")}
          </span>
          <div className="min-w-0 flex-1">
            <h4 className="ident truncate text-xs font-semibold text-ink" title={leg.provider}>
              {leg.provider}
            </h4>
            <Ident className="block truncate text-[11px] text-ink-dim" title={leg.model}>
              {leg.model}
            </Ident>
          </div>
        </div>

        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 flex-wrap items-center gap-1">
            {leg.active && <Badge tone="live">primary</Badge>}
            {leg.disabled && <Badge tone="parked">disabled</Badge>}
            {leg.provider_disabled && !leg.disabled && <Badge tone="parked">parked</Badge>}
          </div>

          <div className="flex shrink-0 items-center gap-1">
            <Button
              variant="icon"
              aria-label={`Move ${leg.provider} earlier`}
              disabled={controlsDisabled || index === 0}
              onClick={() => onMove("up")}
            >
              <ArrowUp className="size-3" />
            </Button>
            <Button
              variant="icon"
              aria-label={`Move ${leg.provider} later`}
              disabled={controlsDisabled || index === chainLength - 1}
              onClick={() => onMove("down")}
            >
              <ArrowDown className="size-3" />
            </Button>
            <Switch
              checked={!disabled}
              disabled={controlsDisabled || leg.provider_disabled}
              onCheckedChange={(checked) => onToggle(!checked)}
              label={`Use ${leg.provider} for ${modelId}`}
              size="sm"
              className="ml-1"
            />
          </div>
        </div>
      </div>

      {!last && (
        <div className="flex h-5 items-center justify-center lg:hidden" aria-hidden>
          <div className="relative flex h-full w-full items-center justify-center">
            <span className="absolute h-full w-px bg-line-strong" />
            <ArrowDown className="relative size-3 bg-panel text-ink-faint" />
          </div>
        </div>
      )}
    </li>
  )
}

function routeKey(chain: RouteLeg[], index: number): string {
  const leg = chain[index]
  let occurrence = 0
  for (let current = 0; current < index; current += 1) {
    if (chain[current].provider === leg.provider && chain[current].model === leg.model) {
      occurrence += 1
    }
  }

  return `${leg.provider}\u0000${leg.model}\u0000${occurrence}`
}
