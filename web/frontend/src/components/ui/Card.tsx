import type { ReactNode } from 'react'

export function Card({ title, action, children, className = '' }: { title?: string; action?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-xl border border-slate-800 bg-slate-900 ${className}`}>
      {(title || action) && (
        <header className="flex items-center justify-between border-b border-slate-800 px-5 py-3">
          {title && <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-400">{title}</h3>}
          {action}
        </header>
      )}
      <div className="p-5">{children}</div>
    </section>
  )
}
