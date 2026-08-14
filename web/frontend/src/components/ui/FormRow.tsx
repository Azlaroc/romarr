import type { ReactNode } from 'react'

/** Two-column label/control layout for the settings editors. */
export function FormRow({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="grid gap-2 border-b border-slate-800/60 py-4 last:border-b-0 sm:grid-cols-[200px_1fr] sm:gap-6">
      <div>
        <div className="text-sm font-medium text-slate-200">{label}</div>
        {hint && <div className="mt-0.5 text-xs text-slate-500">{hint}</div>}
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}
