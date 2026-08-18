import { useEffect, useRef, useState, type ReactNode } from 'react'
import { HelpCircle } from 'lucide-react'

/**
 * The (?) beside a field label — how an arr explains a setting without putting
 * a paragraph in the form.
 */
export function InfoPopover({ label, children }: { label?: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLSpanElement>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <span className="relative inline-flex" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="text-slate-600 transition-colors hover:text-slate-300"
        aria-label={label ? `Help: ${label}` : 'More information'}
        aria-expanded={open}
        data-testid="info-popover-trigger"
      >
        <HelpCircle className="h-3.5 w-3.5" />
      </button>
      {open && (
        <span
          role="tooltip"
          className="absolute bottom-full left-1/2 z-40 mb-1.5 w-64 -translate-x-1/2 rounded border border-slate-700 bg-slate-800 p-2.5 text-xs font-normal leading-relaxed text-slate-300 shadow-xl"
          data-testid="info-popover-panel"
        >
          {children}
        </span>
      )}
    </span>
  )
}
