import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Library, RefreshCw } from 'lucide-react'
import { PageShell } from '../../components/layout/PageShell'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { DataTable, type Column } from '../../components/ui/DataTable'
import { InfoPopover } from '../../components/ui/InfoPopover'
import { inputCls } from '../../components/ui/Input'
import { useToast } from '../../components/ui/Toast'
import { useCollectionTargets, useSyncCollection, usePlatformRegistry } from '../../api/queries'
import type { CollectionTarget } from '../../api/types'
import { summariseSync } from '../../lib/collection'

/** Status reads as a state of the WORK, not a judgement of the game. */
const STATUS: Record<string, { label: string; color: 'slate' | 'accent' | 'emerald' | 'orange' }> = {
  wanted: { label: 'Wanted', color: 'accent' },
  grabbed: { label: 'Grabbed', color: 'emerald' },
  unavailable: { label: 'Not found yet', color: 'slate' },
}

export function Collection() {
  const [platform, setPlatform] = useState('')
  const [status, setStatus] = useState('all')
  const { data, isLoading } = useCollectionTargets({ platform, status })
  const { data: platformRows } = usePlatformRegistry()
  const sync = useSyncCollection()
  const { toast } = useToast()

  const nameOf = (slug: string) =>
    platformRows?.find((p) => p.slug === slug)?.display_name ?? slug

  const onSync = async () => {
    try {
      const res = await sync.mutateAsync(platform || undefined)
      toast(summariseSync(res.results), res.results.length ? 'success' : 'info')
    } catch {
      toast('Could not update the gap list.', 'error')
    }
  }

  const columns: Column<CollectionTarget>[] = [
    {
      key: 'title',
      header: 'Game',
      sortValue: (r) => r.title.toLowerCase(),
      render: (r) => (
        <div>
          <div className="text-slate-200">{r.title}</div>
          {r.dump_name && r.dump_name !== r.title && (
            <div className="text-[11px] text-slate-500">{r.dump_name}</div>
          )}
        </div>
      ),
    },
    {
      key: 'platform',
      header: 'Platform',
      sortValue: (r) => r.platform_slug,
      render: (r) => (
        <Link
          to={`/platforms`}
          className="text-xs text-slate-400 underline decoration-dotted hover:text-slate-200"
        >
          {nameOf(r.platform_slug)}
        </Link>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      sortValue: (r) => r.status,
      render: (r) => {
        const s = STATUS[r.status] ?? { label: r.status, color: 'slate' as const }
        return <Badge color={s.color}>{s.label}</Badge>
      },
    },
    {
      key: 'attempts',
      header: 'Tries',
      align: 'right',
      sortValue: (r) => r.attempts,
      render: (r) => <span className="text-xs text-slate-400">{r.attempts || '—'}</span>,
    },
    {
      key: 'reason',
      header: 'Last result',
      sortValue: (r) => r.last_reason ?? '',
      render: (r) => (
        <div className="max-w-md">
          <div className="truncate text-xs text-slate-400" title={r.last_reason}>
            {r.last_reason || 'Not searched yet'}
          </div>
          {r.last_attempt && <div className="text-[11px] text-slate-600">{r.last_attempt}</div>}
        </div>
      ),
    },
  ]

  const counts = data?.counts ?? {}
  const total = data?.total ?? 0

  return (
    <PageShell
      title="Collection"
      subtitle="What the monitored sets are missing"
      actions={
        <Button onClick={onSync} disabled={sync.isPending} data-testid="coll-sync">
          <RefreshCw className="mr-1.5 h-4 w-4" />
          {sync.isPending ? 'Updating…' : 'Update gap list'}
        </Button>
      }
    >
      <div className="mb-4 flex flex-wrap items-center gap-3" data-testid="coll-filters">
        <select
          className={`${inputCls} w-56`}
          value={platform}
          onChange={(e) => setPlatform(e.target.value)}
          aria-label="Platform"
          data-testid="coll-platform"
        >
          <option value="">All platforms in collection mode</option>
          {(data?.platforms ?? []).map((slug) => (
            <option key={slug} value={slug}>
              {nameOf(slug)}
            </option>
          ))}
        </select>
        <select
          className={`${inputCls} w-44`}
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          aria-label="Status"
          data-testid="coll-status"
        >
          <option value="all">Any status</option>
          <option value="wanted">Wanted</option>
          <option value="grabbed">Grabbed</option>
          <option value="unavailable">Not found yet</option>
        </select>
        <div className="text-xs text-slate-500">
          {total} {total === 1 ? 'gap' : 'gaps'}
          {counts.grabbed ? ` · ${counts.grabbed} grabbed` : ''}
          {counts.unavailable ? ` · ${counts.unavailable} not found yet` : ''}
          {' · '}
          <span data-testid="coll-pace">up to {data?.fill_per_cycle ?? 0} searched per cycle</span>{' '}
          <InfoPopover label="Collection mode">
            A platform in collection mode monitors its whole 1G1R set — one dump per game, chosen by that
            platform&apos;s quality profile and the preservation catalogs. Everything the set wants and the library
            does not have shows up here, and the scheduler works through it a few at a time so one switch cannot
            empty a whole catalog into an indexer at once. A gap nothing carries yet backs off rather than being
            searched every cycle; the last result column says what happened.
          </InfoPopover>
        </div>
      </div>

      <DataTable<CollectionTarget>
        columns={columns}
        rows={data?.targets ?? []}
        rowKey={(r) => String(r.id)}
        loading={isLoading}
        initialSort={{ key: 'title' }}
        testId="coll-table"
        empty={{
          icon: Library,
          title: 'No gaps',
          hint: 'Turn collection mode on for a platform under Platforms, then update the gap list.',
        }}
      />
    </PageShell>
  )
}
