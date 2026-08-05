import { useState } from 'react'
import { Inbox, RotateCcw, Trash2, X, FolderInput } from 'lucide-react'
import { useDownloads, useQueueAction } from '../../api/queries'
import type { DownloadItem } from '../../api/types'
import { PageHeader } from '../../components/layout/PageHeader'
import { StatusPill } from '../../components/ui/StatusPill'
import { Button } from '../../components/ui/Button'
import { EmptyState } from '../../components/ui/EmptyState'
import { Spinner } from '../../components/ui/Spinner'
import { Modal } from '../../components/ui/Modal'
import { PlatformSelect, usePlatformName } from '../PlatformSelect'
import { useToast } from '../../components/ui/Toast'
import { formatEta } from '../../lib/format'

export function Queue() {
  const { data: downloads, isLoading } = useDownloads(5000)
  const actions = useQueueAction()
  const { toast } = useToast()
  const platformName = usePlatformName()

  const [organizeTarget, setOrganizeTarget] = useState<DownloadItem | null>(null)
  const [organizePlatform, setOrganizePlatform] = useState('pc')

  const confirmOrganize = async () => {
    if (!organizeTarget?.hash) return
    const pid = organizePlatform
    const isPc = pid.toLowerCase() === 'pc'
    try {
      await actions.organize.mutateAsync({
        hash: organizeTarget.hash,
        platform: isPc ? 'PC' : platformName(pid),
        platform_slug: isPc ? '' : pid,
        is_pc: isPc,
      })
      if (organizeTarget.job_id) await actions.removeJob.mutateAsync(organizeTarget.job_id)
      toast('Organizing…', 'success')
    } catch {
      toast('Failed to organize', 'error')
    } finally {
      setOrganizeTarget(null)
    }
  }

  const remove = async (d: DownloadItem) => {
    if (d.hash) {
      await actions.removeTorrent.mutateAsync(d.hash)
      if (d.job_id) await actions.removeJob.mutateAsync(d.job_id)
    } else if (d.job_id) {
      await actions.removeJob.mutateAsync(d.job_id)
    }
  }

  const list = downloads ?? []

  return (
    <>
      <PageHeader
        title="Activity"
        subtitle="Download queue"
        actions={
          <Button variant="secondary" onClick={() => actions.clear.mutate()} disabled={actions.clear.isPending}>
            <Trash2 className="h-4 w-4" /> Clear finished
          </Button>
        }
      />

      {isLoading ? (
        <div className="py-16">
          <Spinner label="Loading queue…" className="justify-center" />
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon={Inbox} title="No active downloads" />
      ) : (
        <div className="space-y-3" data-testid="downloads">
          {list.map((d, i) => {
            const hasProg = d.progress != null
            const eta = formatEta(d.eta)
            const canOrganize = d.status === 'completed_unorganized' && !!d.hash
            const canRetry = ['error', 'interrupted', 'dead_letter'].includes(d.status) && !!d.job_id
            return (
              <div key={d.hash || d.job_id || i} className="rounded-xl border border-slate-800 bg-slate-900 p-4">
                <div className="mb-2 flex items-start justify-between gap-3">
                  <span className="flex-1 break-words text-sm font-medium text-white">{d.title}</span>
                  <div className="flex flex-shrink-0 gap-1.5">
                    {canOrganize && (
                      <Button size="sm" variant="secondary" onClick={() => { setOrganizeTarget(d); setOrganizePlatform('pc') }}>
                        <FolderInput className="h-3.5 w-3.5" /> Organize
                      </Button>
                    )}
                    {canRetry && (
                      <Button size="sm" variant="secondary" onClick={() => d.job_id && actions.retry.mutate(d.job_id)}>
                        <RotateCcw className="h-3.5 w-3.5" /> Retry
                      </Button>
                    )}
                    <Button size="sm" variant="danger" onClick={() => remove(d)}>
                      <X className="h-3.5 w-3.5" /> {d.hash ? 'Remove' : 'Dismiss'}
                    </Button>
                  </div>
                </div>
                <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-slate-500">
                  <StatusPill status={d.status} />
                  {d.platform && <span>{d.platform}</span>}
                  {d.size && d.size !== '?' && <span>{d.size}</span>}
                  {d.speed && !d.speed.startsWith('0') && <span>{d.speed}</span>}
                  {eta && <span>ETA {eta}</span>}
                  {hasProg && <span className="text-slate-400">{d.progress}%</span>}
                </div>
                {hasProg && (
                  <div className="h-1.5 overflow-hidden rounded-full bg-slate-800">
                    <div
                      className={`h-full rounded-full transition-[width] ${
                        (d.progress ?? 0) >= 100 ? 'bg-emerald-500' : d.status === 'error' ? 'bg-red-500' : 'bg-accent-500'
                      }`}
                      style={{ width: `${d.progress}%` }}
                    />
                  </div>
                )}
                {d.detail && <div className="mt-1.5 text-xs text-slate-500">{d.detail}</div>}
                {d.error && <div className="mt-1 text-xs text-red-400">{d.error}</div>}
              </div>
            )
          })}
        </div>
      )}

      <Modal open={!!organizeTarget} onClose={() => setOrganizeTarget(null)} title="Organize download">
        <div className="space-y-4">
          <p className="text-sm text-slate-400">Pick the platform to import “{organizeTarget?.title}” into.</p>
          <PlatformSelect value={organizePlatform} onChange={setOrganizePlatform} includeAll={false} className="w-full" />
          <div className="flex justify-end gap-2 border-t border-slate-800 pt-4">
            <Button variant="secondary" onClick={() => setOrganizeTarget(null)}>Cancel</Button>
            <Button onClick={confirmOrganize} disabled={actions.organize.isPending}>Organize</Button>
          </div>
        </div>
      </Modal>
    </>
  )
}
