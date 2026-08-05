import type { LucideIcon } from 'lucide-react'

export function EmptyState({ icon: Icon, title, hint }: { icon: LucideIcon; title: string; hint?: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center text-slate-500">
      <Icon className="mb-3 h-10 w-10 text-slate-700" />
      <p className="text-sm">{title}</p>
      {hint && <p className="mt-1 text-xs text-slate-600">{hint}</p>}
    </div>
  )
}
