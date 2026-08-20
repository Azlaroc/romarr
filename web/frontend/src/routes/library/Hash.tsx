import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowLeft, Fingerprint, Play, Square } from 'lucide-react'
import { useHashStatus, useHashResults, useHashRun, useHashStop } from '../../api/queries'
import { isForbidden } from '../../api/client'
import type { HashfillRow } from '../../api/types'
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

const STATUS: Record<string, { color: BadgeColor; label: string }> = {
  hashed: { color: 'emerald', label: 'hashed' },
  skip: { color: 'slate', label: 'skipped' },
  error: { color: 'red', label: 'error' },
}

function statusOf(row: HashfillRow) {
  return STATUS[row.status] ?? { color: 'slate' as BadgeColor, label: row.status }
}

function humanBytes(n: number) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  return `${(n / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function LibraryHash() {
  const [scope, setScope] = useState('all')
  const [dryRun, setDryRun] = useState(true)
  const [force, setForce] = useState(false)
  const [page, setPage] = useState(1)
  const [confirmOpen, setConfirmOpen] = useState(false)

  const status = useHashStatus()
  const running = status.data?.running === true
  // A run that visited nothing still ran, so the counters come out on
  // finished_at rather than on a non-zero count.
  const hasRun = Boolean(status.data?.finished_at)
  const results = useHashResults(page, hasRun && !running)
  const run = useHashRun()
  const stop = useHashStop()
  const { toast } = useToast()

  if (isForbidden(status.error)) {
    return (
      <>
        <PageHeader title="Hashes" subtitle="Give library files that carry no hash a hash" />
        <AdminNotice />
      </>
    )
  }

  const start = async () => {
    setConfirmOpen(false)
    setPage(1)
    try {
      const res = await run.mutateAsync({ platformSlug: scope, dryRun, force })
      if (res.success) toast(dryRun ? 'Dry run started — nothing will be written.' : 'Hashing…', 'success')
      else toast(res.error || 'Failed to start', 'error')
    } catch {
      toast('Failed to start', 'error')
    }
  }

  const pending = status.data?.pending ?? {}
  const pendingAll = status.data?.pending_all ?? 0
  const pendingList = Object.entries(pending).sort((a, b) => b[1] - a[1])
  const rows = results.data?.items ?? []
  const total = results.data?.total ?? 0

  return (
    <>
      <PageHeader
        title="Hashes"
        subtitle="Ownership decided by proof, not by a guess at the title"
        actions={
          <Link
            to="/"
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm font-medium text-slate-300 hover:bg-slate-700"
          >
            <ArrowLeft className="h-3.5 w-3.5" /> Library
          </Link>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-3" data-testid="hash-controls">
        <PlatformSelect value={scope} onChange={setScope} testid="hash-platform" />
        <Button
          onClick={() => (force ? setConfirmOpen(true) : void start())}
          disabled={running || run.isPending}
          data-testid="hash-run-btn"
        >
          <Play className="h-3.5 w-3.5" /> {dryRun ? 'Dry run' : 'Hash'}
        </Button>
        {running && (
          <Button variant="danger" onClick={() => stop.mutate()} data-testid="hash-stop-btn">
            <Square className="h-3.5 w-3.5" /> Stop
          </Button>
        )}
        <Toggle
          checked={dryRun}
          onChange={setDryRun}
          label="Dry run"
          hint="Hash everything and write nothing — the way to check the result against a catalog before committing to it"
          data-testid="hash-dry-run"
        />
        <Toggle
          checked={force}
          onChange={setForce}
          label="Re-hash everything"
          hint="Also re-visit rows that already carry a hash or were permanently skipped. Only needed after the way hashes are derived changes."
          data-testid="hash-force"
        />
      </div>

      <div className="mb-4 flex flex-wrap gap-x-5 gap-y-1 text-sm text-slate-400" data-testid="hash-status">
        <span>{running ? 'Running…' : hasRun ? 'Last run finished' : 'Idle'}</span>
        {hasRun && (
          <>
            <span>
              {status.data?.done ?? 0} of {status.data?.total ?? 0} visited
            </span>
            <span>{status.data?.hashed ?? 0} hashed</span>
            <span>{status.data?.stripped ?? 0} header-stripped</span>
            <span>{status.data?.skipped ?? 0} skipped</span>
            <span className={status.data?.errors ? 'text-red-400' : ''}>{status.data?.errors ?? 0} errors</span>
            {/* Bytes, because a platform whose work is a few very large files
                leaves the row counter sitting still for minutes. */}
            <span>{humanBytes(status.data?.bytes_hashed ?? 0)} read</span>
            {status.data?.dry_run ? <Badge color="yellow">dry run — nothing written</Badge> : null}
          </>
        )}
        {status.data?.last_error ? <span className="text-red-400">{status.data.last_error}</span> : null}
        <InfoPopover label="Hashes">
          Ownership is decided in three tiers — a hash is proof, a canonical filename is strong, a parsed title is a
          guess — so a file with no stored hash is weakly matched everywhere: the gap list, the owned check, the
          duplicate gate, and the declutter, which refuses to archive anything matched only by its title. This reads
          each file (the ROM <em>inside</em> an archive, not the archive) and stores what it finds. Nothing is moved,
          renamed or deleted. Where a platform wraps its dumps in a container header, the header-stripped hash is
          stored alongside — that is the one its catalog actually publishes.
        </InfoPopover>
      </div>

      {pendingAll > 0 && (
        <div className="mb-4 rounded-lg border border-slate-800 bg-slate-900 p-3" data-testid="hash-pending">
          <div className="mb-2 text-sm text-slate-300">
            {pendingAll} file{pendingAll === 1 ? '' : 's'} still carry no hash
          </div>
          <div className="flex flex-wrap gap-2">
            {pendingList.map(([slug, n]) => (
              <button
                key={slug}
                onClick={() => setScope(slug)}
                className="rounded-md border border-slate-700 bg-slate-800 px-2 py-1 font-mono text-xs text-slate-300 hover:bg-slate-700"
                data-testid={`hash-pending-${slug}`}
              >
                {slug} <span className="text-slate-500">{n}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      {!hasRun && !running && pendingAll === 0 && (
        <EmptyState
          icon={Fingerprint}
          title="Every file has a hash"
          hint="Nothing to do. Rows that can never have one — directories, multi-file archives — are not counted here."
        />
      )}

      {!hasRun && !running && pendingAll > 0 && (
        <EmptyState
          icon={Fingerprint}
          title="No run yet"
          hint="Pick a platform (or all) and start. A dry run writes nothing, so it is safe to look first."
        />
      )}

      {hasRun && !running && rows.length === 0 && (
        <EmptyState icon={Fingerprint} title="Nothing to show" hint="The run visited no rows for this scope." />
      )}

      {rows.length > 0 && (
        <div className="space-y-2" data-testid="hash-results">
          {rows.map((r) => {
            const st = statusOf(r)
            return (
              <div
                key={r.library_id}
                data-testid={`hash-row-${r.library_id}`}
                className="rounded-lg border border-slate-800 bg-slate-900 p-3"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Badge color={st.color}>{st.label}</Badge>
                  <span className="font-mono text-sm text-slate-300">{r.name}</span>
                  {r.header && <Badge color="purple">{r.header} header</Badge>}
                </div>
                <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-slate-500">
                  {r.md5 && (
                    <span className="font-mono" title="The file's own bytes — its identity">
                      md5 {r.md5}
                    </span>
                  )}
                  {r.unh_md5 && (
                    <span className="font-mono text-slate-400" title="Header stripped — what the catalog publishes">
                      unh {r.unh_md5}
                    </span>
                  )}
                  {r.reason && <span>{r.reason}</span>}
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
        title="Re-hash everything"
        message={`Re-visit every file in ${scope === 'all' ? 'the whole library' : scope}, including ones that already have a hash. This reads every byte and can take a long time. Only needed when the way hashes are derived has changed.`}
        confirmLabel="Re-hash"
        busy={run.isPending}
        onConfirm={() => void start()}
        onCancel={() => setConfirmOpen(false)}
      />
    </>
  )
}
