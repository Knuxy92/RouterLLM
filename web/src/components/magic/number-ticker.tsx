import { cn } from "@/lib/utils"

type NumberTickerProps = {
  value: number
  className?: string
  decimalPlaces?: number
}

function formatNumber(value: number, decimalPlaces: number): string {
  return Intl.NumberFormat("en-US", {
    minimumFractionDigits: decimalPlaces,
    maximumFractionDigits: decimalPlaces,
  }).format(Number(value.toFixed(decimalPlaces)))
}

export function NumberTicker({ value, className, decimalPlaces = 0 }: NumberTickerProps) {
  return (
    <span className={cn("inline-block tabular-nums", className)}>
      {formatNumber(value, decimalPlaces)}
    </span>
  )
}
