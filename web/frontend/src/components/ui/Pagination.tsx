import { ChevronLeft, ChevronRight } from 'lucide-react'

export function Pagination({ page, totalPages, onChange }: { page: number; totalPages: number; onChange: (p: number) => void }) {
  if (totalPages <= 1) return null
  const from = Math.max(1, page - 3)
  const to = Math.min(totalPages, page + 3)
  const pages: number[] = []
  for (let p = from; p <= to; p++) pages.push(p)

  const cls = (active: boolean) =>
    `flex h-8 min-w-8 items-center justify-center rounded px-2 text-sm ${
      active ? 'bg-accent-600 font-semibold text-white' : 'border border-slate-700 bg-slate-800 text-slate-300 hover:bg-slate-700'
    }`

  return (
    <nav className="mt-6 flex items-center justify-center gap-1.5" aria-label="Pagination">
      <button className={cls(false)} disabled={page <= 1} onClick={() => onChange(page - 1)} aria-label="Previous">
        <ChevronLeft className="h-4 w-4" />
      </button>
      {pages.map((p) => (
        <button key={p} className={cls(p === page)} onClick={() => onChange(p)}>
          {p}
        </button>
      ))}
      <button className={cls(false)} disabled={page >= totalPages} onClick={() => onChange(page + 1)} aria-label="Next">
        <ChevronRight className="h-4 w-4" />
      </button>
    </nav>
  )
}
