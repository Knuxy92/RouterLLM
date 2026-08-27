import { memo, useEffect, useMemo, useRef, useState } from "react"
import { Pause, Play } from "lucide-react"

import type { LogEntry } from "@/lib/api"
import { Badge, Button, Panel, PanelHeader } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"

type LogPanelProps = {
  entries: LogEntry[]
  paused: boolean
  onPausedChange: (paused: boolean) => void
}

type ParsedLog = {
  time: string
  level: "info" | "ready" | "warn" | "error"
  message: string
}

const levelTone = {
  info: "neutral",
  ready: "live",
  warn: "cooling",
  error: "dead",
} as const

export const LogPanel = memo(function LogPanel({ entries, paused, onPausedChange }: LogPanelProps) {
  const [filter, setFilter] = useState("")
  const scrollRef = useRef<HTMLDivElement>(null)

  const visible = useMemo(() => {
    if (!filter.trim()) return entries
    const needle = filter.toLowerCase()

    return entries.filter((entry) => entry.line.toLowerCase().includes(needle))
  }, [entries, filter])

  useEffect(() => {
    if (paused || !scrollRef.current) return
    scrollRef.current.scrollTop = scrollRef.current.scrollHeight
  }, [visible, paused])

  return (
    <Panel>
      <PanelHeader
        title="Logs"
        meta={`${visible.length} line${visible.length === 1 ? "" : "s"}`}
        action={
          <div className="flex w-full items-center gap-2 sm:w-auto">
            <input
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              placeholder="filter"
              aria-label="Filter log lines"
              className="ident min-w-0 flex-1 rounded border border-line-strong bg-inset px-2 py-1 text-[11px] text-ink placeholder:text-ink-ghost focus:border-live/50 focus:outline-2 focus:outline-offset-2 focus:outline-ring sm:w-40 sm:flex-none"
            />
            <Button
              aria-label={paused ? "Resume log updates" : "Pause log updates"}
              onClick={() => onPausedChange(!paused)}
            >
              {paused ? <Play className="size-3" /> : <Pause className="size-3" />}
              {paused ? "Resume" : "Pause"}
            </Button>
          </div>
        }
      />
      <div
        ref={scrollRef}
        role="log"
        aria-live={paused ? "off" : "polite"}
        aria-relevant="additions"
        className="h-52 overflow-y-auto bg-inset px-4 py-3 sm:h-56"
      >
        {visible.length === 0 ? (
          <p className="text-[11px] text-ink-faint">
            {entries.length === 0 ? "No log output captured yet." : "No lines match this filter."}
          </p>
        ) : (
          <ul className="space-y-1 text-[11px] leading-relaxed">
            {visible.map((entry) => {
              const parsed = parseLog(entry.line)

              return (
                <li key={entry.seq} className="grid grid-cols-[4.5rem_3.25rem_minmax(0,1fr)] items-baseline gap-2">
                  <time className="ident text-ink-dim">{parsed.time}</time>
                  <Badge tone={levelTone[parsed.level]} className="justify-center">
                    {parsed.level}
                  </Badge>
                  <span className={cn("ident min-w-0 break-words", messageTone(parsed.level))}>
                    {parsed.message}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </Panel>
  )
})

function parseLog(line: string): ParsedLog {
  const match = line.match(/^\d{4}\/\d{2}\/\d{2}\s+(\d{2}:\d{2}:\d{2})\s+(.*)$/)
  const message = match?.[2] ?? line

  return {
    time: match?.[1] ?? "--:--:--",
    level: logLevel(message),
    message,
  }
}

function logLevel(message: string): ParsedLog["level"] {
  if (/rejected|dead|error|failed|exhausted|panic/i.test(message)) return "error"
  if (/retry|transient|stale|cooldown|skipped/i.test(message)) return "warn"
  if (/reloaded|serving|listening|loaded/i.test(message)) return "ready"

  return "info"
}

function messageTone(level: ParsedLog["level"]): string {
  if (level === "error") return "text-dead"
  if (level === "warn") return "text-cooling"
  if (level === "ready") return "text-live"

  return "text-ink-dim"
}
