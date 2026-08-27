import type { ProviderStatus } from "@/lib/api"
import { Badge, Ident } from "@/components/ui/primitives"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

type ProviderCardProps = {
  provider: ProviderStatus
  pending: boolean
  controlsDisabled: boolean
  onToggle: (disabled: boolean) => void
}

export function ProviderCard({ provider, pending, controlsDisabled, onToggle }: ProviderCardProps) {
  const parked = provider.disabled
  const allDead = !parked && provider.keys_total > 0 && provider.keys_alive === 0

  return (
    <article
      className={cn(
        "relative flex flex-col gap-3 rounded-lg border bg-panel p-4 transition-opacity",
        parked ? "border-parked/30 opacity-60" : "border-line",
        allDead && "border-dead/40",
        pending && "opacity-70",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="ident truncate text-sm font-semibold text-ink">{provider.name}</h3>
          <Ident className="mt-0.5 block truncate text-[11px] text-ink-dim" title={provider.base_url}>
            {provider.base_url}
          </Ident>
        </div>
        <Switch
          checked={!parked}
          disabled={controlsDisabled}
          onCheckedChange={(next) => onToggle(!next)}
          label={`Use provider ${provider.name}`}
        />
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        <Badge>{provider.style}</Badge>
        <Badge>{provider.auth_mode}</Badge>
        {provider.share && <Badge>share: {provider.share}</Badge>}
        {parked && <Badge tone="parked">parked</Badge>}
        {allDead && <Badge tone="dead">no live keys</Badge>}
      </div>

      <KeyStrip provider={provider} />

      <dl className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-ink-dim">
        <div className="flex gap-1">
          <dt>models</dt>
          <dd className="ident text-ink-dim">{provider.model_count}</dd>
        </div>
        <div className="flex gap-1">
          <dt>reqs</dt>
          <dd className="ident text-ink-dim">{provider.requests}</dd>
        </div>
        <div className="flex gap-1">
          <dt>errors</dt>
          <dd className={cn("ident", provider.errors > 0 ? "text-dead" : "text-ink-dim")}>
            {provider.errors}
          </dd>
        </div>
      </dl>
    </article>
  )
}

function KeyStrip({ provider }: { provider: ProviderStatus }) {
  if (provider.keys_total === 0) {
    return <p className="text-[11px] text-ink-faint">no keys configured</p>
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="flex flex-wrap items-center gap-0.5">
        {provider.keys.map((key) => (
          <Tooltip key={key.masked}>
            <TooltipTrigger asChild>
              <button
                type="button"
                aria-label={
                  key.alive
                    ? `Key ${key.masked}: alive`
                    : `Key ${key.masked}: dead, ${key.cooldown_left_seconds} seconds cooldown remaining`
                }
                className="inline-flex size-6 items-center justify-center rounded focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring"
              >
                <span
                  className={cn(
                    "size-2 rounded-full",
                    key.alive ? "bg-live" : "bg-dead",
                  )}
                />
              </button>
            </TooltipTrigger>
            <TooltipContent side="top">
              <span className="ident">{key.masked}</span>
              {key.alive ? " — alive" : ` — dead, ${key.cooldown_left_seconds}s left`}
            </TooltipContent>
          </Tooltip>
        ))}
        {provider.keys.length === 0 &&
          Array.from({ length: provider.keys_total }).map((_, index) => (
            <span key={index} className="inline-flex size-6 items-center justify-center" aria-hidden>
              <span className="size-2 rounded-full bg-parked/50" />
            </span>
          ))}
      </div>
      <span className="ident text-[11px] text-ink-faint">
        {provider.serving ? `${provider.keys_alive}/${provider.keys_total} alive` : `${provider.keys_total} keys`}
      </span>
    </div>
  )
}
