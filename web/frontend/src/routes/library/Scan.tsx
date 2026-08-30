import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowLeft, FolderSearch, Play, Square } from 'lucide-react'
import { useScanStatus, useScanResults, useScanRun, useScanStop } from '../../api/queries'
import { isForbidden } from '../../api/client'
import type { LibscanRow } from '../../api/types'
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
  created: { color: 'emerald', label: 'created' },
  adopted: { color: 'blue', label: 'adopted' },
  missing: { color: 'yellow', label: 'missing' },
  unvisited: { color: 'orange', label: 'unvisited' },
  'unknown-platform': { color: 'purple', label: 'unknown platform' },
  unsorted: { color: 'slate', label: 'unsorted' },
  error: { color: 'red', label: 'error' },
}

const CATALOG: Record<string, BadgeColor> = {
  verified: 'emerald',
  mismatch: 'red',
  unknown: 'slate',
}

function statusOf(row: LibscanRow) {
  return STATUS[row.status] ?? { color: 'slate' as BadgeColor, label: row.status }
}

function humanBytes(n: number) {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  return `${(n / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function LibraryScan() {
  const [scope, setScope] = useState('all')
  const [dryRun, setDryRun] = useState(true)
  const [force, setForce] = useState(false)
  const [page, setPage] = useState(1)
  const [confirmOpen, setConfirmOpen] = useState(false)

  const status = useScanStatus()
  const running = status.data?.running === true
  // A run that saw nothing still ran, so the counters come out on
  // finished_at rather than on a non-zero count.
  const hasRun = Boolean(status.data?.finished_at)
  const results = useScanResults(page, hasRun && !running)
  const run = useScanRun()
  const stop = useScanStop()
  const { toast } = useToast()

  if (isForbidden(status.error)) {
    return (
      <>
        <PageHeader title="Scan" subtitle="Reconcile the ROM tree with the library" />
        <AdminNotice />
      </>
    )
  }

  const start = async () => {
    setConfirmOpen(false)
    setPage(1)
    try {
      const res = await run.mutateAsync({ platformSlug: scope, dryRun, force })
      if (res.success) toast(dryRun ? 'Dry run started — nothing will be written.' : 'Scanning…', 'success')
      else toast(res.error || 'Failed to start', 'error')
    } catch {
      toast('Failed to start', 'error')
    }
  }

  const rows = results.data?.items ?? []
  const total = results.data?.total ?? 0

  return (
    <>
      <PageHeader
        title="Scan"
        subtitle="The library owns its own folders: adopt, create, report — never delete"
        actions={
          <Link
            to="/"
            className="inline-flex items-center gap-1.5 rounded-lg border border-slate-700 bg-slate-800 px-3 py-2 text-sm font-medium text-slate-300 hover:bg-slate-700"
          >
            <ArrowLeft className="h-3.5 w-3.5" /> Library
          </Link>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-3" data-testid="scan-controls">
        <PlatformSelect value={scope} onChange={setScope} testid="scan-platform" />
        <Button
          onClick={() => (force ? setConfirmOpen(true) : void start())}
          disabled={running || run.isPending}
          data-testid="scan-run-btn"
        >
          <Play className="h-3.5 w-3.5" /> {dryRun ? 'Dry run' : 'Scan'}
        </Button>
        {running && (
          <Button variant="danger" onClick={() => stop.mutate()} data-testid="scan-stop-btn">
            <Square className="h-3.5 w-3.5" /> Stop
          </Button>
        )}
        <Toggle
          checked={dryRun}
          onChange={setDryRun}
          label="Dry run"
          hint="Walk and measure everything, write nothing — the way to see what a first scan would do before letting it do it"
          data-testid="scan-dry-run"
        />
        <Toggle
          checked={force}
          onChange={setForce}
          label="Re-measure everything"
          hint="Also re-measure files whose rows already carry hashes or a verdict. A verified verdict is never downgraded to unknown."
          data-testid="scan-force"
        />
      </div>

      <div className="mb-4 flex flex-wrap gap-x-5 gap-y-1 text-sm text-slate-400" data-testid="scan-status">
        <span>{running ? `Running (${status.data?.phase})…` : hasRun ? 'Last run finished' : 'Idle'}</span>
        {hasRun && (
          <>
            <span>
              {status.data?.done ?? 0} of {status.data?.total ?? 0} visited
            </span>
            <span>{status.data?.created ?? 0} created</span>
            <span>{status.data?.adopted ?? 0} adopted</span>
            <span className={status.data?.missing ? 'text-yellow-400' : ''}>{status.data?.missing ?? 0} missing</span>
            <span className={status.data?.unvisited ? 'text-orange-400' : ''}>
              {status.data?.unvisited ?? 0} unvisited
            </span>
            <span className={status.data?.errors ? 'text-red-400' : ''}>{status.data?.errors ?? 0} errors</span>
            <span>{humanBytes(status.data?.bytes_hashed ?? 0)} read</span>
            {status.data?.dry_run ? <Badge color="yellow">dry run — nothing written</Badge> : null}
          </>
        )}
        {status.data?.last_error ? <span className="text-red-400">{status.data.last_error}</span> : null}
        <InfoPopover label="Scan">
          The library&apos;s inventory comes from its own root folders. A file a row already tracks is{' '}
          <em>adopted</em> — the row keeps its history and gains a catalog verdict when its stored hashes can
          answer for free; rows with no stored measurement are counted and left alone (the routine scan never
          reads file bytes for adopted rows — re-measure, per platform, is the way to fill them). A file no row
          tracks becomes a new row, hashed and judged. A row whose file is gone is only <em>reported</em>: this
          scan deletes nothing, moves nothing, renames nothing. Platforms come from the top-level directory name
          checked against the registry — never guessed from a file&apos;s extension.
        </InfoPopover>
      </div>

      {!hasRun && !running && (
        <EmptyState
          icon={FolderSearch}
          title="No scan yet"
          hint="Pick a platform (or all) and start. A dry run writes nothing, so it is safe to look first."
        />
      )}

      {hasRun && !running && rows.length === 0 && (
        <EmptyState icon={FolderSearch} title="Nothing to show" hint="The scan saw nothing for this scope." />
      )}

      {rows.length > 0 && (
        <div className="space-y-2" data-testid="scan-results">
          {rows.map((r, i) => {
            const st = statusOf(r)
            return (
              <div
                key={`${r.path}-${i}`}
                data-testid={`scan-row-${r.library_id ?? i}`}
                className="rounded-lg border border-slate-800 bg-slate-900 p-3"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Badge color={st.color}>{st.label}</Badge>
                  <span className="font-mono text-sm text-slate-300">{r.name}</span>
                  {r.catalog && <Badge color={CATALOG[r.catalog] ?? 'slate'}>{r.catalog}</Badge>}
                </div>
                <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-slate-500">
                  {r.detail && <span>{r.detail}</span>}
                  <span className="font-mono text-slate-600">{r.path}</span>
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
        title="Re-measure everything"
        message={`Re-measure every file in ${scope === 'all' ? 'the whole ROM tree' : scope}, including ones whose rows already carry hashes and verdicts. This reads every byte and can take a long time.`}
        confirmLabel="Re-measure"
        busy={run.isPending}
        onConfirm={() => void start()}
        onCancel={() => setConfirmOpen(false)}
      />
    </>
  )
}
