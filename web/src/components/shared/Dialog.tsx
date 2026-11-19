import { useEffect, useRef, type ReactNode } from "react"

interface DialogProps {
  open: boolean
  onClose: () => void
  title?: ReactNode
  className?: string
  children: ReactNode
}

export default function Dialog({ open, onClose, title, className = "leg-dialog", children }: DialogProps) {
  const ref = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

  return (
    <dialog
      ref={ref}
      className={className}
      onCancel={(e) => {
        e.preventDefault()
        onClose()
      }}
      onClick={(e) => {
        if (e.target === ref.current) onClose()
      }}
    >
      <div className="p-5">
        {title && <h3 className="mb-1 text-sm font-semibold">{title}</h3>}
        {children}
      </div>
    </dialog>
  )
}
