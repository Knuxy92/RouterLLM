import { useEffect, type ReactNode } from "react"
import { XIcon } from "../icons"

interface DrawerProps {
  open: boolean
  onClose: () => void
  title?: ReactNode
  children: ReactNode
}

export default function Drawer({ open, onClose, title, children }: DrawerProps) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose()
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [open, onClose])

  return (
    <>
      <div
        className={`scrim fixed inset-0 z-40 bg-black/50 ${open ? "open" : ""}`}
        onClick={onClose}
      ></div>
      <aside
        className={`drawer fixed inset-y-0 right-0 z-50 flex w-full max-w-xl flex-col border-l bg-card shadow-xl ${open ? "open" : ""}`}
        role="dialog"
        aria-modal="true"
      >
        <div className="flex h-14 items-center justify-between border-b px-4">
          <div className="flex items-center gap-2">
            <p className="text-sm font-semibold">{title}</p>
          </div>
          <button
            type="button"
            className="rounded-md border p-1.5 hover:bg-accent"
            onClick={onClose}
          >
            <XIcon className="size-4" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto px-4 py-4">{children}</div>
      </aside>
    </>
  )
}
