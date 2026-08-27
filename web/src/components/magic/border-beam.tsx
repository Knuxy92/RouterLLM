import { cn } from "@/lib/utils"

type BorderBeamProps = {
  className?: string
  duration?: number
  size?: number
  color?: string
}

/**
 * A single light travelling the border of its container. Used only on the
 * currently-serving route leg, so it always means "traffic goes here".
 */
export function BorderBeam({
  className,
  duration = 7,
  size = 90,
  color = "var(--live)",
}: BorderBeamProps) {
  return (
    <div
      aria-hidden
      className={cn(
        "pointer-events-none absolute inset-0 rounded-[inherit]",
        "[border:1px_solid_transparent] ![mask-clip:padding-box,border-box] ![mask-composite:intersect]",
        "[mask:linear-gradient(transparent,transparent),linear-gradient(#000,#000)]",
        className,
      )}
    >
      <div
        className="absolute aspect-square animate-[border-beam_var(--beam-duration)_linear_infinite]"
        style={
          {
            width: size,
            offsetPath: `rect(0 auto auto 0 round ${size}px)`,
            background: `linear-gradient(to left, ${color}, transparent)`,
            "--beam-duration": `${duration}s`,
          } as React.CSSProperties
        }
      />
    </div>
  )
}
