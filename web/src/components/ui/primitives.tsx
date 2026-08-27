import type { ComponentProps, ReactNode } from "react"

import { cn } from "@/lib/utils"

export function Panel({ className, children, ...props }: ComponentProps<"section">) {
  return (
    <section className={cn("rounded-lg border border-line bg-panel", className)} {...props}>
      {children}
    </section>
  )
}

type PanelHeaderProps = {
  title: string
  meta?: ReactNode
  action?: ReactNode
}

export function PanelHeader({ title, meta, action }: PanelHeaderProps) {
  return (
    <header className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-4 py-2.5">
      <h2 className="text-[11px] font-semibold tracking-[0.14em] text-ink-dim uppercase">
        {title}
      </h2>
      <div className="rack hidden h-3 min-w-8 flex-1 opacity-50 sm:block" aria-hidden />
      {meta && <span className="text-xs text-ink-dim">{meta}</span>}
      {action && <div className="basis-full sm:ml-auto sm:basis-auto">{action}</div>}
    </header>
  )
}

type BadgeTone = "live" | "cooling" | "dead" | "parked" | "neutral"

const toneClass: Record<BadgeTone, string> = {
  live: "border-live/40 bg-live-bg text-live",
  cooling: "border-cooling/40 bg-cooling-bg text-cooling",
  dead: "border-dead/40 bg-dead-bg text-dead",
  parked: "border-parked/40 bg-parked-bg text-parked",
  neutral: "border-line-strong bg-raised text-ink-dim",
}

export function Badge({
  tone = "neutral",
  className,
  children,
}: {
  tone?: BadgeTone
  className?: string
  children: ReactNode
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium tracking-wide uppercase",
        toneClass[tone],
        className,
      )}
    >
      {children}
    </span>
  )
}

type ButtonVariant = "ghost" | "solid" | "icon"

export function Button({
  className,
  variant = "ghost",
  ...props
}: ComponentProps<"button"> & { variant?: ButtonVariant }) {
  return (
    <button
      type="button"
      className={cn(
        "inline-flex min-h-6 min-w-6 items-center justify-center gap-1.5 rounded border text-xs font-medium transition-colors",
        "disabled:cursor-not-allowed disabled:opacity-45",
        variant === "solid" && "border-live/50 bg-live-bg px-2.5 py-1 text-live hover:bg-live/20",
        variant === "ghost" && "border-line-strong bg-raised px-2.5 py-1 text-ink-dim hover:border-ink-ghost hover:text-ink",
        variant === "icon" && "border-line bg-inset p-1 text-ink-dim hover:border-line-strong hover:bg-raised hover:text-ink",
        className,
      )}
      {...props}
    />
  )
}

export function Ident({ className, ...props }: ComponentProps<"span">) {
  return <span className={cn("ident text-ink-dim", className)} {...props} />
}

export function EmptyState({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="px-4 py-8 text-center">
      <p className="text-sm font-medium text-ink-dim">{title}</p>
      <p className="mt-1 text-[11px] text-ink-faint">{detail}</p>
    </div>
  )
}
