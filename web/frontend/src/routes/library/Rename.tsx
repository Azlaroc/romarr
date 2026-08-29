import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ArrowLeft, Play, Square, Wand2, X, Undo2 } from 'lucide-react'
import {
  useNormalizeStatus,
  useNormalizeResults,
  useNormalizePreview,
  useNormalizeApply,
  useNormalizeStop,
} from '../../api/queries'
import { isForbidden } from '../../api/client'
import type { NormalizePreviewRow } from '../../api/types'
import { PageHeader } from '../../components/layout/PageHeader'
import { PlatformSelect } from '../PlatformSelect'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge, type BadgeColor } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { EmptyState } from '../../components/ui/EmptyState'
import { Pagination } from '../../components/ui/Pagination'
import { useToast } from '../../components/ui/Toast'

function verdictBadge(verdict?: string): { color: BadgeColor; label: string } {
  if (verdict === 'byte-identical') return { color: 'red', label: 'byte-identical dupe' }
  if (verdict === 'different-bytes') return { color: 'yellow', label: 'different bytes — 1G1R call' }
  return { color: 'blue', label: 'unknown bytes' }
}

// sourceBadge renders which authority proposed a row's name: the local DAT
// snapshot (the decider) or the online Playmatch fallback (review-only).
export function sourceBadge(source?: string): { color: BadgeColor; label: string } | null {
  if (source === 'playmatch') return { color: 'yellow', label: 'Playmatch' }
  if (source === 'dat') return { color: 'blue', label: 'DAT' }
  return null
}

function statusBadge(status: NormalizePreviewRow['status']): BadgeColor {
  switch (status) {
    case 'rename':
      return 'purple'
    case 'renamed':
      return 'emerald'
    case 'noop':
      return 'blue'
    case 'review':
      return 'yellow'
    default:
      return 'yellow'
  }
}

export function LibraryRename() {
  const [params] = useSearchParams()
  const [scope, setScope] = useState(params.get('platform') || 'all')
  const [page, setPage] = useState(1)
  const [excluded, setExcluded] = useState<Set<number>>(new Set())
  const [confirmOpen, setConfirmOpen] = useState(false)

  const status = useNormalizeStatus()
  const running = status.data?.running === true
  const phase = status.data?.phase ?? 'idle'
  const hasRun = phase !== 'idle'
  const results = useNormalizeResults(page, hasRun && !running)
  const preview = useNormalizePreview()
  const apply = useNormalizeApply()
  const stop = useNormalizeStop()
  const { toast } = useToast()

  if (isForbidden(status.error)) {
    return (
      <>
        <PageHeader title="Rename" subtitle="Bulk-normalize library names" />
        <AdminNotice />
      </>
    )
  }

  const startPreview = async () => {
    setExcluded(new Set())
    setPage(1)
    try {
      const res = await preview.mutateAsync(scope)
      if (res.success) toast('Preview started', 'success')
      else toast(res.error || 'Failed to start', 'error')
    } catch {
      toast('Failed to start', 'error')
    }
  }

  const startApply = async () => {
    setConfirmOpen(false)
    try {
      const res = await apply.mutateAsync([...excluded])
      if (res.success) toast('Applying renames…', 'success')
      else toast(res.error || 'Failed to start', 'error')
    } catch {
      toast('Failed to start', 'error')
    }
  }

  const toggleExcluded = (id: number) => {
    setExcluded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const planned = status.data?.planned ?? 0
  const applyCount = Math.max(0, planned - excluded.size)
  const rows = results.data?.items ?? []
  const total = results.data?.total ?? 0
  const counters = status.data

  return (
    <>
      <PageHeader
        title="Rename"
        subtitle="Preview and apply DAT-canonical names across the library"
        actions={
          <Link to="/" className="inline-flex items-center gap-1.5 rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm font-medium text-slate-300 hover:bg-slate-700">
            <ArrowLeft className="h-3.5 w-3.5" /> Library
          </Link>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-3" data-testid="rename-controls">
        <PlatformSelect value={scope} onChange={setScope} testid="rename-platform" />
        <Button onClick={() => void startPreview()} disabled={running || preview.isPending} data-testid="rename-preview-btn">
          <Play className="h-3.5 w-3.5" /> Preview
        </Button>
        <Button
          variant="secondary"
          onClick={() => setConfirmOpen(true)}
          disabled={running || applyCount === 0}
          data-testid="rename-apply-btn"
        >
          <Wand2 className="h-3.5 w-3.5" /> Apply {applyCount > 0 ? `(${applyCount})` : ''}
        </Button>
        {running && (
          <Button variant="danger" onClick={() => stop.mutate()} data-testid="rename-stop-btn">
            <Square className="h-3.5 w-3.5" /> Stop
          </Button>
        )}
      </div>

      <div className="mb-4 flex flex-wrap gap-x-5 gap-y-1 text-sm text-slate-400" data-testid="rename-status">
        <span>
          {running ? `Running ${phase}…` : hasRun ? `Last ${phase} finished` : 'Idle'}
        </span>
        {hasRun && (
          <>
            <span>{counters?.done ?? 0}/{counters?.total ?? 0} scanned</span>
            <span>{planned} to rename</span>
            <span>{counters?.renamed ?? 0} renamed</span>
            <span>{counters?.skipped ?? 0} skipped</span>
            <span className={counters?.collisions ? 'text-yellow-400' : ''}>{counters?.collisions ?? 0} collisions</span>
            <span className={counters?.reviews ? 'text-yellow-400' : ''}>{counters?.reviews ?? 0} to review</span>
            <span className={counters?.dat_misses ? 'text-orange-400' : ''} data-testid="rename-dat-misses">{counters?.dat_misses ?? 0} no local DAT match</span>
            {(counters?.source_playmatch ?? 0) > 0 && (
              <span className="text-yellow-400">{counters?.source_playmatch} named via Playmatch</span>
            )}
            <span className={counters?.errors ? 'text-red-400' : ''}>{counters?.errors ?? 0} errors</span>
          </>
        )}
        {counters?.last_error ? <span className="text-red-400">{counters.last_error}</span> : null}
      </div>

      {!hasRun && !running && (
        <EmptyState
          icon={Wand2}
          title="No preview yet"
          hint="Pick a platform (or all) and run a preview — nothing is renamed until you apply."
        />
      )}

      {hasRun && !running && rows.length === 0 && (
        <EmptyState icon={Wand2} title="Nothing to show" hint="The preview produced no rows for this scope." />
      )}

      {rows.length > 0 && (
        <div className="space-y-2" data-testid="rename-results">
          {rows.map((r) => {
            const excl = excluded.has(r.library_id)
            const src = sourceBadge(r.name_source)
            return (
              <div
                key={r.library_id}
                data-testid={`rename-row-${r.library_id}`}
                className={`rounded-lg border border-slate-800 bg-slate-900 p-3 ${r.status === 'noop' || excl ? 'opacity-50' : ''}`}
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Badge color={statusBadge(r.status)}>{excl ? 'excluded' : r.status}</Badge>
                  <span className="font-mono text-sm text-slate-300">{r.old_name}</span>
                  {r.new_name && r.status !== 'noop' && (
                    <>
                      <span className="text-slate-600">→</span>
                      <span className="font-mono text-sm text-white">{r.new_name}</span>
                    </>
                  )}
                  {src && r.status !== 'noop' && <Badge color={src.color}>{src.label}</Badge>}
                  {r.status === 'rename' && (
                    <button
                      onClick={() => toggleExcluded(r.library_id)}
                      className="ml-auto text-slate-500 hover:text-slate-300"
                      title={excl ? 'Include in apply' : 'Exclude from apply'}
                      data-testid={`rename-toggle-${r.library_id}`}
                    >
                      {excl ? <Undo2 className="h-4 w-4" /> : <X className="h-4 w-4" />}
                    </button>
                  )}
                </div>
                {(r.reason || r.collision) && (
                  <div className="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-slate-500">
                    {r.reason && <span>{r.reason}</span>}
                    {r.collision && (
                      <>
                        <Badge color={verdictBadge(r.collision.verdict).color}>
                          {verdictBadge(r.collision.verdict).label}
                        </Badge>
                        <span>vs {r.collision.with_name}</span>
                      </>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      {total > 100 && (
        <div className="mt-4">
          <Pagination page={page} totalPages={Math.ceil(total / 100)} onChange={setPage} />
        </div>
      )}

      <ConfirmDialog
        open={confirmOpen}
        title="Apply renames"
        message={`Rename ${applyCount} file${applyCount === 1 ? '' : 's'} to their DAT-canonical names? Files with name collisions are skipped and reported, never overwritten.`}
        confirmLabel={`Rename ${applyCount}`}
        busy={apply.isPending}
        onConfirm={() => void startApply()}
        onCancel={() => setConfirmOpen(false)}
      />
    </>
  )
}
