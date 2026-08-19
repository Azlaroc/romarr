import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Archive, ArrowLeft, Play, Square, Undo2, X } from 'lucide-react'
import {
  usePruneStatus,
  usePruneResults,
  usePrunePreview,
  usePruneApply,
  usePruneStop,
} from '../../api/queries'
import { isForbidden } from '../../api/client'
import type { PrunePreviewRow } from '../../api/types'
import { PageHeader } from '../../components/layout/PageHeader'
import { PlatformSelect } from '../PlatformSelect'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge, type BadgeColor } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { EmptyState } from '../../components/ui/EmptyState'
import { InfoPopover } from '../../components/ui/InfoPopover'
import { Pagination } from '../../components/ui/Pagination'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

/** Each verdict says what it is AND why it is not simply "delete this". */
const VERDICT: Record<string, { color: BadgeColor; label: string }> = {
  archive: { color: 'purple', label: 'surplus' },
  review: { color: 'yellow', label: 'review' },
  'excluded-group': { color: 'orange', label: 'off-catalog group' },
  'uncatalogued-duplicate': { color: 'yellow', label: 'off-catalog spare' },
  uncatalogued: { color: 'blue', label: 'not in any catalog' },
}

function verdictOf(row: PrunePreviewRow) {
  return VERDICT[row.verdict] ?? { color: 'slate' as BadgeColor, label: row.verdict }
}

export function LibraryDeclutter() {
  const [scope, setScope] = useState('all')
  const [includeExcluded, setIncludeExcluded] = useState(false)
  const [includeDupes, setIncludeDupes] = useState(false)
  const [page, setPage] = useState(1)
  const [excluded, setExcluded] = useState<Set<number>>(new Set())
  const [confirmOpen, setConfirmOpen] = useState(false)

  const status = usePruneStatus()
  const running = status.data?.running === true
  const phase = status.data?.phase ?? 'idle'
  const hasRun = phase !== 'idle'
  const results = usePruneResults(page, hasRun && !running)
  const preview = usePrunePreview()
  const apply = usePruneApply()
  const stop = usePruneStop()
  const { toast } = useToast()

  if (isForbidden(status.error)) {
    return (
      <>
        <PageHeader title="Declutter" subtitle="Prune the surplus a monitored set does not keep" />
        <AdminNotice />
      </>
    )
  }

  const startPreview = async () => {
    setExcluded(new Set())
    setPage(1)
    try {
      const res = await preview.mutateAsync({ platformSlug: scope, includeExcluded, includeDupes })
      if (res.success) toast('Preview started — nothing moves until you apply.', 'success')
      else toast(res.error || 'Failed to start', 'error')
    } catch {
      toast('Failed to start', 'error')
    }
  }

  const startApply = async () => {
    setConfirmOpen(false)
    try {
      const res = await apply.mutateAsync([...excluded])
      if (res.success) toast('Archiving…', 'success')
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

  const planned = status.data?.total ?? 0
  const applyCount = Math.max(0, planned - excluded.size)
  const rows = results.data?.items ?? []
  const total = results.data?.total ?? 0
  const counts = status.data?.counts ?? {}

  return (
    <>
      <PageHeader
        title="Declutter"
        subtitle="The 1G1R set, read in the other direction"
        actions={
          <Link
            to="/"
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm font-medium text-slate-300 hover:bg-slate-700"
          >
            <ArrowLeft className="h-3.5 w-3.5" /> Library
          </Link>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-3" data-testid="prune-controls">
        <PlatformSelect value={scope} onChange={setScope} testid="prune-platform" />
        <Button onClick={() => void startPreview()} disabled={running || preview.isPending} data-testid="prune-preview-btn">
          <Play className="h-3.5 w-3.5" /> Preview
        </Button>
        <Button
          variant="secondary"
          onClick={() => setConfirmOpen(true)}
          disabled={running || applyCount === 0}
          data-testid="prune-apply-btn"
        >
          <Archive className="h-3.5 w-3.5" /> Archive {applyCount > 0 ? `(${applyCount})` : ''}
        </Button>
        {running && (
          <Button variant="danger" onClick={() => stop.mutate()} data-testid="prune-stop-btn">
            <Square className="h-3.5 w-3.5" /> Stop
          </Button>
        )}
        <Toggle
          checked={includeExcluded}
          onChange={setIncludeExcluded}
          label="Include off-catalog groups"
          hint="Catalogued hacks, prototypes and unlicensed dumps — archiving one removes a game, not a duplicate"
          data-testid="prune-include-excluded"
        />
        <Toggle
          checked={includeDupes}
          onChange={setIncludeDupes}
          label="Include off-catalog spares"
          hint="Files no catalog knows whose title is a game you already own the catalogued dump of — the hack and alt-dump pile"
          data-testid="prune-include-dupes"
        />
      </div>

      <div className="mb-4 flex flex-wrap gap-x-5 gap-y-1 text-sm text-slate-400" data-testid="prune-status">
        <span>{running ? `Running ${phase}…` : hasRun ? `Last ${phase} finished` : 'Idle'}</span>
        {hasRun && (
          <>
            <span>{planned} to archive</span>
            <span>{status.data?.archived ?? 0} archived</span>
            <span className={counts.review ? 'text-yellow-400' : ''}>{counts.review ?? 0} to review</span>
            <span className={counts['excluded-group'] ? 'text-orange-400' : ''}>
              {counts['excluded-group'] ?? 0} off-catalog
            </span>
            <span className={counts['uncatalogued-duplicate'] ? 'text-yellow-400' : ''}>
              {counts['uncatalogued-duplicate'] ?? 0} off-catalog spares
            </span>
            <span>{status.data?.uncatalogued ?? 0} not in any catalog</span>
            <span className={status.data?.errors ? 'text-red-400' : ''}>{status.data?.errors ?? 0} errors</span>
          </>
        )}
        {status.data?.last_error ? <span className="text-red-400">{status.data.last_error}</span> : null}
        <InfoPopover label="Declutter">
          Every file listed here is <strong>moved</strong> into {status.data?.archive_root || 'the archive'}, never
          deleted, and a manifest records where each one came from and what replaced it. A dump is only surplus when
          the one the set actually keeps is already on disk — if the keeper is still missing, the copy you have is
          the collection and it is left alone. Files no catalog has heard of are counted and listed but never
          archived — unless its title is a game you already own the catalogued dump of, which is the hack and
          alt-dump pile and has its own opt-in.
        </InfoPopover>
      </div>

      {!hasRun && !running && (
        <EmptyState
          icon={Archive}
          title="No preview yet"
          hint="Pick a platform (or all) and run a preview — nothing moves until you apply."
        />
      )}

      {hasRun && !running && rows.length === 0 && (
        <EmptyState icon={Archive} title="Nothing to show" hint="The preview produced no rows for this scope." />
      )}

      {rows.length > 0 && (
        <div className="space-y-2" data-testid="prune-results">
          {rows.map((r) => {
            const excl = excluded.has(r.library_id)
            const v = verdictOf(r)
            const actionable = r.status === 'planned'
            return (
              <div
                key={r.library_id}
                data-testid={`prune-row-${r.library_id}`}
                className={`rounded-lg border border-slate-800 bg-slate-900 p-3 ${!actionable || excl ? 'opacity-60' : ''}`}
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Badge color={v.color}>{excl ? 'excluded' : v.label}</Badge>
                  {r.status === 'archived' && <Badge color="emerald">archived</Badge>}
                  <span className="font-mono text-sm text-slate-300">{r.name}</span>
                  {r.keeper && (
                    <>
                      <span className="text-slate-600">keeps</span>
                      <span className="font-mono text-sm text-white">{r.keeper}</span>
                    </>
                  )}
                  {actionable && (
                    <button
                      onClick={() => toggleExcluded(r.library_id)}
                      className="ml-auto text-slate-500 hover:text-slate-300"
                      title={excl ? 'Include in archive' : 'Exclude from archive'}
                      data-testid={`prune-toggle-${r.library_id}`}
                    >
                      {excl ? <Undo2 className="h-4 w-4" /> : <X className="h-4 w-4" />}
                    </button>
                  )}
                </div>
                <div className="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-slate-500">
                  {r.reason && <span>{r.reason}</span>}
                  {r.matched_by && <Badge color="slate">matched by {r.matched_by}</Badge>}
                  <span className="text-slate-600">{r.platform_slug}</span>
                </div>
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
        title="Archive surplus"
        message={`Move ${applyCount} file${applyCount === 1 ? '' : 's'} into ${status.data?.archive_root || 'the archive'}? Nothing is deleted — each move is recorded in a manifest, and the file can be moved back.`}
        confirmLabel={`Archive ${applyCount}`}
        busy={apply.isPending}
        onConfirm={() => void startApply()}
        onCancel={() => setConfirmOpen(false)}
      />
    </>
  )
}
