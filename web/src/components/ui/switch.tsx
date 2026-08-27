import { cn } from "@/lib/utils"

type SwitchSize = "sm" | "md"

/**
 * Track and knob geometry are paired per size. The knob is positioned in
 * absolute pixels, so a caller that resizes only the track via className
 * would push the knob outside it — hence `size` rather than a free className.
 */
const sizeClass: Record<SwitchSize, { track: string; knob: string; off: string; on: string }> = {
  sm: { track: "h-6 w-10", knob: "size-3.5", off: "left-[3px]", on: "left-[23px]" },
  md: { track: "h-6 w-11", knob: "size-4", off: "left-[3px]", on: "left-[25px]" },
}

type SwitchProps = {
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  label: string
  size?: SwitchSize
  className?: string
}

export function Switch({
  checked,
  onCheckedChange,
  disabled,
  label,
  size = "md",
  className,
}: SwitchProps) {
  const geometry = sizeClass[size]

  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onCheckedChange(!checked)}
      className={cn(
        "relative shrink-0 rounded-full border transition-colors duration-150",
        "disabled:cursor-not-allowed disabled:opacity-50",
        geometry.track,
        checked ? "border-live/50 bg-live/25" : "border-line-strong bg-raised",
        className,
      )}
    >
      <span
        className={cn(
          "absolute top-1/2 -translate-y-1/2 rounded-full transition-[left,background-color] duration-150",
          geometry.knob,
          checked ? `${geometry.on} bg-live` : `${geometry.off} bg-ink-ghost`,
        )}
      />
    </button>
  )
}
