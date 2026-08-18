import { useMemo, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronUp } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { Skeleton } from './Skeleton'
import { EmptyState } from './EmptyState'
import { Pagination } from './Pagination'

export interface Column<T> {
  key: string
  header: ReactNode
  render: (row: T) => ReactNode
  /** Supply to make the column sortable. */
  sortValue?: (row: T) => string | number
  align?: 'left' | 'right'
  /** Extra classes for both the header cell and the body cells. */
  className?: string
}

type SortDir = 'asc' | 'desc'

/**
 * The arr data table: dense hoverable rows, sortable headers, right-aligned
 * row actions, with loading / empty / paged states wired in.
 *
 * Skeleton, EmptyState and Pagination all existed before this and nothing
 * rendered a table with them — every list in the app was a stack of divs. This
 * is the one place that knows how those three fit together.
 *
 * Sorting is client-side and only offered for columns that declare a
 * sortValue; pagination is controlled by the caller, so a server-paged screen
 * and a locally-paged one use the same component.
 */
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  loading = false,
  empty,
  initialSort,
  page,
  totalPages,
  onPageChange,
  testId,
}: {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  loading?: boolean
  empty?: { icon: LucideIcon; title: string; hint?: string }
  initialSort?: { key: string; dir?: SortDir }
  page?: number
  totalPages?: number
  onPageChange?: (p: number) => void
  testId?: string
}) {
  const [sortKey, setSortKey] = useState<string | undefined>(initialSort?.key)
  const [sortDir, setSortDir] = useState<SortDir>(initialSort?.dir ?? 'asc')

  const sorted = useMemo(() => {
    const col = columns.find((c) => c.key === sortKey && c.sortValue)
    if (!col?.sortValue) return rows
    const get = col.sortValue
    const factor = sortDir === 'asc' ? 1 : -1
    return [...rows].sort((a, b) => {
      const av = get(a)
      const bv = get(b)
      if (typeof av === 'number' && typeof bv === 'number') return (av - bv) * factor
      return String(av).localeCompare(String(bv)) * factor
    })
  }, [columns, rows, sortKey, sortDir])

  const toggleSort = (key: string) => {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  if (loading) {
    return (
      <div className="space-y-2" data-testid={testId ? `${testId}-loading` : 'datatable-loading'}>
        {[0, 1, 2, 3, 4].map((i) => (
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    )
  }

  if (!sorted.length && empty) {
    return (
      <div data-testid={testId ? `${testId}-empty` : 'datatable-empty'}>
        <EmptyState icon={empty.icon} title={empty.title} hint={empty.hint} />
      </div>
    )
  }

  return (
    <div>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-sm" data-testid={testId}>
          <thead>
            <tr className="border-b border-slate-700 text-left text-xs uppercase tracking-wide text-slate-500">
              {columns.map((c) => {
                const sortable = Boolean(c.sortValue)
                const active = sortable && c.key === sortKey
                return (
                  <th
                    key={c.key}
                    scope="col"
                    className={`px-3 py-2 font-medium ${c.align === 'right' ? 'text-right' : 'text-left'} ${c.className ?? ''}`}
                  >
                    {sortable ? (
                      <button
                        type="button"
                        onClick={() => toggleSort(c.key)}
                        className={`inline-flex items-center gap-1 hover:text-slate-300 ${active ? 'text-slate-300' : ''}`}
                        data-testid={`sort-${c.key}`}
                        aria-label={`Sort by ${c.key}`}
                      >
                        {c.header}
                        {active &&
                          (sortDir === 'asc' ? <ChevronUp className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />)}
                      </button>
                    ) : (
                      c.header
                    )}
                  </th>
                )
              })}
            </tr>
          </thead>
          <tbody>
            {sorted.map((row) => (
              <tr
                key={rowKey(row)}
                className="border-b border-slate-800/60 last:border-b-0 hover:bg-[rgba(255,255,255,0.08)]"
                data-testid={`row-${rowKey(row)}`}
              >
                {columns.map((c) => (
                  <td
                    key={c.key}
                    className={`px-3 py-2 align-middle ${c.align === 'right' ? 'text-right' : ''} ${c.className ?? ''}`}
                  >
                    {c.render(row)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {page !== undefined && totalPages !== undefined && onPageChange && (
        <Pagination page={page} totalPages={totalPages} onChange={onPageChange} />
      )}
    </div>
  )
}
