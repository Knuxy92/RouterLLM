import { AlertTriangle, X } from "lucide-react"

import type { ReloadStatus } from "@/lib/api"
import { Button, Ident } from "@/components/ui/primitives"

type ReloadBannerProps = {
  reload: ReloadStatus
  configPath: string
  onDismiss: () => void
}

/**
 * The server only logs a rejected reload to stdout. Without this banner a bad
 * save is silent: the file looks edited, the router keeps the old config, and
 * nothing on screen explains why.
 */
export function ReloadBanner({ reload, configPath, onDismiss }: ReloadBannerProps) {
  if (reload.ok || !reload.error) return null

  return (
    <div
      role="alert"
      className="flex items-start gap-3 rounded-lg border border-dead/45 bg-dead-bg px-4 py-3"
    >
      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-dead" />
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium text-dead">Last config reload was rejected</p>
        <Ident className="mt-1 block break-words text-[11px] text-ink-dim">{reload.error}</Ident>
        <p className="mt-1.5 text-[11px] text-ink-faint">
          The previous working config is still serving. Fix <Ident>{configPath}</Ident> and save
          again.
        </p>
      </div>
      <Button aria-label="Dismiss reload error" onClick={onDismiss} className="px-1.5 py-1">
        <X className="size-3" />
      </Button>
    </div>
  )
}
