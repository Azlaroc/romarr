import { usePlatforms } from '../api/queries'

const selectCls =
  'rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm text-slate-300 focus:border-accent-500 focus:outline-none'

/** Platform <select>. includeAll adds the "all" sentinel from /api/platforms. */
export function PlatformSelect({
  value,
  onChange,
  includeAll = true,
  className = '',
  testid,
}: {
  value: string
  onChange: (v: string) => void
  includeAll?: boolean
  className?: string
  testid?: string
}) {
  const { data: platforms = [] } = usePlatforms()
  const opts = includeAll ? platforms : platforms.filter((p) => p.id !== 'all')
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={`${selectCls} ${className}`}
      data-testid={testid}
    >
      {opts.map((p) => (
        <option key={p.id} value={p.id}>
          {p.name}
        </option>
      ))}
    </select>
  )
}

/** Look up a platform's display name by its slug/id. */
export function usePlatformName() {
  const { data: platforms = [] } = usePlatforms()
  return (id: string) => platforms.find((p) => p.id === id)?.name ?? id
}
