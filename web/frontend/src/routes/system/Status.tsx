import { useDashboard, useStats } from '../../api/queries'
import { isForbidden } from '../../api/client'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge } from '../../components/ui/Badge'
import { Card } from '../../components/ui/Card'
import { Skeleton } from '../../components/ui/Skeleton'

export function SystemStatus() {
  const { data, error, isLoading } = useDashboard()
  const { data: stats } = useStats()

  if (isForbidden(error)) {
    return (
      <>
        <PageHeader title="System" subtitle="Status" />
        <AdminNotice />
      </>
    )
  }

  const health = data?.sources_health ?? []
  const sys = data?.system ?? {}

  return (
    <>
      <PageHeader title="System" subtitle="Status" />
      <div className="space-y-6">
        <Card title="Health">
          {isLoading ? (
            <Skeleton className="h-16 rounded-lg" />
          ) : (
            <div className="flex flex-wrap gap-2" data-testid="system-health">
              {health.map((h) => (
                <Badge key={h.name} color={h.status === 'ok' ? 'emerald' : 'yellow'}>
                  {h.label}: {h.status === 'ok' ? 'ok' : 'not configured'}
                </Badge>
              ))}
              {health.length === 0 && <span className="text-sm text-slate-500">No health data.</span>}
            </div>
          )}
        </Card>

        <Card title="About">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4" data-testid="system-about">
            <Tile label="Version" value={sys.version ?? '—'} />
            <Tile label="Uptime" value={sys.uptime ?? '—'} />
            <Tile label="Go" value={sys.go_version ?? '—'} />
            <Tile label="Active downloads" value={String(data?.active_downloads ?? 0)} />
          </div>
        </Card>

        <Card title="Collection stats">
          <CollectionBars
            platforms={stats?.platforms ?? data?.library_stats ?? {}}
            total={stats?.library_total ?? data?.library_total ?? 0}
            jobs={stats?.total_jobs}
          />
        </Card>
      </div>
    </>
  )
}

function Tile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded bg-slate-800 p-3 text-center">
      <div className="truncate text-lg font-bold text-slate-200">{value}</div>
      <div className="mt-1 text-xs text-slate-500">{label}</div>
    </div>
  )
}

/** Relocated from Settings (PR-G) — per-platform library bars. */
function CollectionBars({ platforms, total, jobs }: { platforms: Record<string, number>; total: number; jobs?: number }) {
  const max = Math.max(1, ...Object.values(platforms))
  const sorted = Object.entries(platforms).sort((a, b) => b[1] - a[1])
  return (
    <div data-testid="system-stats">
      <div className="mb-4 grid grid-cols-2 gap-3">
        <div className="rounded bg-slate-800 p-3 text-center">
          <div className="text-2xl font-bold text-accent-400">{total}</div>
          <div className="mt-1 text-xs text-slate-500">Library items</div>
        </div>
        <div className="rounded bg-slate-800 p-3 text-center">
          <div className="text-2xl font-bold text-slate-300">{jobs ?? 0}</div>
          <div className="mt-1 text-xs text-slate-500">Total jobs</div>
        </div>
      </div>
      {sorted.length === 0 ? (
        <p className="text-sm text-slate-500">No library data yet.</p>
      ) : (
        sorted.map(([slug, count]) => (
          <div key={slug} className="mb-2">
            <div className="mb-0.5 flex justify-between text-xs">
              <span className="text-slate-300">{slug}</span>
              <span className="text-slate-500">{count}</span>
            </div>
            <div className="h-1.5 rounded-full bg-slate-800">
              <div className="h-1.5 rounded-full bg-accent-500" style={{ width: `${Math.round((count / max) * 100)}%` }} />
            </div>
          </div>
        ))
      )}
    </div>
  )
}
