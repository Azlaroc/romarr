import type { ReactNode } from 'react'

/**
 * A fieldset section of a settings form: heading, optional description, then
 * FormRow children.
 *
 * Card is a bare surface with no form semantics; this is the piece that makes
 * settings screens read alike.
 */
export function FormGroup({
  title,
  description,
  action,
  children,
  className = '',
  ...rest
}: {
  title: string
  description?: ReactNode
  /** Optional control rendered on the heading row (e.g. a per-section button). */
  action?: ReactNode
  children: ReactNode
  className?: string
  [key: `data-${string}`]: unknown
}) {
  return (
    <fieldset className={`rounded border border-slate-800 bg-slate-900 px-5 pb-2 pt-4 ${className}`} {...rest}>
      <div className="flex flex-wrap items-start justify-between gap-2 border-b border-slate-700 pb-3">
        <div>
          <legend className="text-lg font-light text-slate-300">{title}</legend>
          {description && <p className="mt-0.5 text-xs text-slate-500">{description}</p>}
        </div>
        {action}
      </div>
      <div className="pt-1">{children}</div>
    </fieldset>
  )
}
