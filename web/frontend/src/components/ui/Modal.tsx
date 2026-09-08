import { useEffect, type ReactNode } from 'react'
import { X } from 'lucide-react'

export function Modal({
  open,
  onClose,
  title,
  children,
  size = 'md',
}: {
  open: boolean
  onClose: () => void
  title?: string
  children: ReactNode
  /** 'lg' is for content that is a list rather than a form (interactive search). */
  size?: 'md' | 'lg'
}) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKey)
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.style.overflow = ''
    }
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/60 p-4 backdrop-blur-sm sm:items-center" onMouseDown={onClose}>
      {/* The panel caps its own height and scrolls its BODY. Letting it grow
          instead breaks under the centered overlay: a centered flex child
          taller than the viewport overflows both ends equally, and the top
          half lands in negative scroll space nothing can reach — a long
          release list cut off its first results. Header stays pinned. */}
      <div
        className={`my-8 flex max-h-[calc(100vh-6rem)] w-full flex-col ${size === 'lg' ? 'max-w-4xl' : 'max-w-lg'} animate-slide-in rounded border border-slate-800 bg-slate-900 shadow-2xl`}
        role="dialog"
        aria-modal="true"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <header className="flex shrink-0 items-center justify-between border-b border-slate-800 px-5 py-3">
          <h3 className="text-sm font-semibold text-white">{title}</h3>
          <button onClick={onClose} className="text-slate-500 hover:text-slate-200" aria-label="Close">
            <X className="h-4 w-4" />
          </button>
        </header>
        <div className="min-h-0 overflow-y-auto p-5">{children}</div>
      </div>
    </div>
  )
}
