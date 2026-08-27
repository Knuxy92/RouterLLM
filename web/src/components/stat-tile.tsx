import { NumberTicker } from "@/components/magic/number-ticker"
import { cn } from "@/lib/utils"

type StatTileProps = {
  label: string
  value: number
  suffix?: string
  tone?: "ink" | "live" | "dead" | "parked"
  hint?: string
}

const toneClass = {
  ink: "text-ink",
  live: "text-live",
  dead: "text-dead",
  parked: "text-parked",
}

export function StatTile({ label, value, suffix, tone = "ink", hint }: StatTileProps) {
  return (
    <div className="rounded-lg border border-line bg-panel px-4 py-2.5">
      <p className="text-[10px] font-medium tracking-[0.12em] text-ink-dim uppercase">{label}</p>
      <p className={cn("mt-1 font-mono text-2xl font-semibold", toneClass[tone])}>
        <NumberTicker value={value} />
        {suffix && <span className="ml-1 text-sm font-normal text-ink-faint">{suffix}</span>}
      </p>
      {hint && <p className="mt-0.5 text-[11px] text-ink-faint">{hint}</p>}
    </div>
  )
}
