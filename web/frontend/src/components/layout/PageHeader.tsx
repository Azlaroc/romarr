import type { ReactNode } from 'react'

export function PageHeader({
  title,
  subtitle,
  actions,
  className = 'mb-6',
}: {
  title: string
  subtitle?: string
  actions?: ReactNode
  /** Spacing override. PageShell tightens this because a toolbar follows. */
  className?: string
}) {
  return (
    <div className={`flex flex-wrap items-end justify-between gap-3 ${className}`}>
      <div>
        <h1 className="text-[21px] font-light text-slate-300" data-testid="page-title">
          {title}
        </h1>
        {subtitle && <p className="mt-0.5 text-sm text-slate-500">{subtitle}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}
