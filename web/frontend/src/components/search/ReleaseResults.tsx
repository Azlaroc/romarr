import { useState } from 'react'
import { Check, Download, Search } from 'lucide-react'
import { useDownloadGame } from '../../api/queries'
import { ApiError } from '../../api/client'
import type { SearchResult } from '../../api/types'
import { Badge, type BadgeColor } from '../ui/Badge'
import { Button } from '../ui/Button'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { EmptyState } from '../ui/EmptyState'
import { Skeleton } from '../ui/Skeleton'
import { useToast } from '../ui/Toast'
import { platformBadgeColor } from '../../lib/format'

function safety(score = 50): { color: BadgeColor; label: string } {
  if (score >= 70) return { color: 'emerald', label: 'Safe' }
  if (score >= 40) return { color: 'yellow', label: 'Caution' }
  return { color: 'red', label: 'Risky' }
}

function sourceBadge(r: SearchResult): { color: BadgeColor; label: string } {
  if (r.source_type === 'ddl') return { color: 'purple', label: 'DDL' }
  if (r.download_protocol === 'nzb') return { color: 'orange', label: 'NZB' }
  return { color: 'blue', label: 'Torrent' }
}

/**
 * The one place a release is listed and grabbed. Add New and the Wanted /
 * Library interactive search both render this, so picking a release by hand
 * runs the same grab — sources, profiles, blocklist and duplicate-hash gate —
 * wherever it is started from. A second copy of this list would be a second
 * acquisition path.
 */
export function ReleaseResults({
  results,
  pending,
  columns = 3,
  emptyHint,
}: {
  results: SearchResult[] | null
  pending: boolean
  columns?: 1 | 2 | 3
  emptyHint?: string
}) {
  const [sent, setSent] = useState<Set<number>>(new Set())
  // A 409 duplicate_hash parks the attempt here until the user confirms a
  // forced re-download or cancels.
  const [dupe, setDupe] = useState<{ result: SearchResult; idx: number; message: string } | null>(null)
  const download = useDownloadGame()
  const { toast } = useToast()

  const grid =
    columns === 1
      ? 'grid grid-cols-1 gap-3'
      : columns === 2
        ? 'grid grid-cols-1 gap-3 lg:grid-cols-2'
        : 'grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3'

  const grab = async (r: SearchResult, idx: number, force = false) => {
    try {
      const res = await download.mutateAsync(force ? { ...r, force: true } : r)
      if (res.success) {
        setSent((prev) => new Set(prev).add(idx))
        toast(`Downloading: ${r.title}`, 'success')
      } else {
        toast(res.error || 'Failed to queue', 'error')
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setDupe({ result: r, idx, message: err.message })
        return
      }
      toast('Failed to queue', 'error')
    }
  }

  if (pending) {
    return (
      <div className={grid}>
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-32" />
        ))}
      </div>
    )
  }
  if (results === null) {
    return <EmptyState icon={Search} title="Search for a game to get started" hint={emptyHint} />
  }
  if (results.length === 0) {
    return <EmptyState icon={Search} title="No results found" hint={emptyHint} />
  }

  return (
    <>
      <div className={grid} data-testid="results">
        {results.map((r, i) => {
          const s = safety(r.safety_score)
          const src = sourceBadge(r)
          const isDDL = r.source_type === 'ddl'
          const warnings = (r.safety_warnings ?? []).join(' · ')
          return (
            <div
              key={i}
              className={`rounded-xl border border-slate-800 bg-slate-900 p-4 ${(r.safety_score ?? 50) < 40 ? 'opacity-70' : ''}`}
            >
              <div className="flex items-start gap-3">
                <div className="flex min-w-[58px] flex-col items-center gap-1.5 pt-0.5">
                  <Badge color={platformBadgeColor(r.is_pc)}>{r.platform || '?'}</Badge>
                  <Badge color={src.color}>{src.label}</Badge>
                  <Badge color={s.color}>{s.label}</Badge>
                </div>
                <div className="min-w-0 flex-1">
                  <div className="break-words text-sm font-medium leading-snug text-white">{r.title}</div>
                  <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-xs text-slate-400">
                    {r.indexer && <span>{r.indexer}</span>}
                    {isDDL ? (
                      <span className="text-purple-400">Direct</span>
                    ) : (
                      <>
                        <span className="text-emerald-400">{r.seeders ?? 0} seeds</span>
                        <span>{r.leechers ?? 0} leech</span>
                      </>
                    )}
                    {r.size_human && <span>{r.size_human}</span>}
                  </div>
                  {warnings && <div className="mt-1 text-xs text-red-400/70">{warnings}</div>}
                </div>
                <Button
                  size="sm"
                  variant={sent.has(i) ? 'secondary' : 'primary'}
                  disabled={sent.has(i) || download.isPending}
                  onClick={() => grab(r, i)}
                  data-testid={`dl-btn-${i}`}
                >
                  {sent.has(i) ? <Check className="h-3.5 w-3.5" /> : <Download className="h-3.5 w-3.5" />}
                  {sent.has(i) ? 'Sent' : 'Get'}
                </Button>
              </div>
            </div>
          )
        })}
      </div>

      <ConfirmDialog
        open={dupe !== null}
        title="Already in library"
        message={dupe?.message ?? ''}
        confirmLabel="Download anyway"
        busy={download.isPending}
        onConfirm={() => {
          if (dupe) {
            void grab(dupe.result, dupe.idx, true)
            setDupe(null)
          }
        }}
        onCancel={() => setDupe(null)}
      />
    </>
  )
}
