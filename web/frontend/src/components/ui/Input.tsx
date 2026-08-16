import type { InputHTMLAttributes } from 'react'

/** Shared text/number input styling (extracted from the PR-A screens). */
export const inputCls =
  'w-full rounded border border-slate-700 bg-slate-800 px-4 py-1.5 text-sm text-slate-300 placeholder-slate-500 focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500 disabled:cursor-not-allowed disabled:opacity-60'

interface Props extends InputHTMLAttributes<HTMLInputElement> {
  label?: string
  hint?: string
  error?: string
  /** Arr convention: advanced fields carry the orange label accent. */
  advanced?: boolean
  // Allow data-* passthrough (e.g. data-testid) on this custom component.
  [key: `data-${string}`]: unknown
}

export function Input({ label, hint, error, advanced, className = '', ...rest }: Props) {
  return (
    <label className="block">
      {label && <span className={`mb-1 block text-xs font-medium ${advanced ? 'text-[#ff902b]' : 'text-slate-400'}`}>{label}</span>}
      <input className={`${inputCls} ${error ? 'border-red-500/70' : ''} ${className}`} {...rest} />
      {error ? (
        <span className="mt-1 block text-xs text-red-400">{error}</span>
      ) : (
        hint && <span className="mt-1 block text-xs text-slate-600">{hint}</span>
      )}
    </label>
  )
}
