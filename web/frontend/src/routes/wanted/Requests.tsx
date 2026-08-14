import { useState, type FormEvent } from 'react'
import { Check, Download, Inbox, Plus, Search, Trash2, XCircle } from 'lucide-react'
import {
  useRequests,
  useCreateRequest,
  useUpdateRequest,
  useDeleteRequest,
  useRequestSearch,
  useRequestDownload,
} from '../../api/queries'
import { ApiError } from '../../api/client'
import type { GameRequest, SearchResult } from '../../api/types'
import { PageHeader } from '../../components/layout/PageHeader'
import { PlatformSelect, usePlatformName } from '../PlatformSelect'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { EmptyState } from '../../components/ui/EmptyState'
import { Input } from '../../components/ui/Input'
import { Modal } from '../../components/ui/Modal'
import { Select } from '../../components/ui/Select'
import { useToast } from '../../components/ui/Toast'

type BadgeColor = 'slate' | 'accent' | 'emerald' | 'blue' | 'purple' | 'orange' | 'red' | 'yellow'

/** Wire statuses from internal/models/request.go; unknown -> neutral. */
const STATUS_COLOR: Record<string, BadgeColor> = {
  pending: 'yellow',
  approved: 'blue',
  searching: 'purple',
  downloading: 'accent',
  completed: 'emerald',
  failed: 'red',
  cancelled: 'slate',
}

const FILTER_OPTIONS = [
  { value: '', label: 'All statuses' },
  ...Object.keys(STATUS_COLOR).map((s) => ({ value: s, label: s[0].toUpperCase() + s.slice(1) })),
]

export function Requests() {
  const [filter, setFilter] = useState('')
  const { data, isLoading } = useRequests(filter)
  const create = useCreateRequest()
  const update = useUpdateRequest()
  const remove = useDeleteRequest()
  const searchReq = useRequestSearch()
  const downloadReq = useRequestDownload()
  const platformName = usePlatformName()
  const { toast } = useToast()

  const [createOpen, setCreateOpen] = useState(false)
  const [title, setTitle] = useState('')
  const [platform, setPlatform] = useState('')
  const [notes, setNotes] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<GameRequest | null>(null)
  // Search-results picker: results arrive tier-sorted from the selector — render in order, never re-sort.
  const [picker, setPicker] = useState<{ req: GameRequest; results: SearchResult[] } | null>(null)

  const requests = data?.requests ?? []

  const submitCreate = async (e: FormEvent) => {
    e.preventDefault()
    const t = title.trim()
    if (!t) {
      toast('Title required', 'error')
      return
    }
    try {
      await create.mutateAsync({
        title: t,
        platform: platform ? platformName(platform) : '',
        platform_slug: platform,
        notes: notes.trim() || undefined,
      })
      setCreateOpen(false)
      setTitle('')
      setNotes('')
      toast('Request created', 'success')
    } catch (err) {
      toast(err instanceof ApiError ? err.message : 'Failed to create request', 'error')
    }
  }

  const patchStatus = async (r: GameRequest, status: string) => {
    try {
      await update.mutateAsync({ id: r.id, status })
      toast(`Request ${status}`, 'success')
    } catch (err) {
      toast(err instanceof ApiError ? err.message : 'Update failed', 'error')
    }
  }

  const runSearch = async (r: GameRequest) => {
    try {
      const resp = await searchReq.mutateAsync(r.id)
      const results = resp.results ?? []
      toast(`${results.length} result${results.length === 1 ? '' : 's'} for “${r.title}”`, 'info')
      if (results.length > 0) setPicker({ req: r, results })
    } catch (err) {
      toast(err instanceof ApiError ? err.message : 'Search failed', 'error')
    }
  }

  const grab = async (result: SearchResult) => {
    if (!picker) return
    try {
      // POST the whole result back — the handler reads the DownloadRequest
      // fields and fills title/platform from the request when absent.
      await downloadReq.mutateAsync({ id: picker.req.id, body: result })
      toast('Download started', 'success')
      setPicker(null)
    } catch (err) {
      toast(err instanceof ApiError ? err.message : 'Download failed', 'error')
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await remove.mutateAsync(deleteTarget.id)
      toast('Request deleted', 'success')
    } catch (err) {
      // DELETE is admin-gated — surface the 403 rather than hiding the button.
      toast(err instanceof ApiError ? err.message : 'Delete failed', 'error')
    } finally {
      setDeleteTarget(null)
    }
  }

  return (
    <>
      <PageHeader
        title="Wanted"
        subtitle="Requests"
        actions={
          <div className="flex items-center gap-2">
            <Select value={filter} onChange={setFilter} options={FILTER_OPTIONS} data-testid="req-filter" />
            <Button onClick={() => setCreateOpen(true)} data-testid="req-add">
              <Plus className="h-4 w-4" /> New request
            </Button>
          </div>
        }
      />

      {/* Container stays mounted when empty so tests can observe rows appearing/shrinking. */}
      <div className="space-y-2" data-testid="requests-list">
        {requests.map((r) => {
          const canApprove = r.status === 'pending' || r.status === 'failed'
          const canCancel = r.status !== 'completed' && r.status !== 'cancelled'
          return (
            <div key={r.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-900 p-4" data-testid={`req-row-${r.id}`}>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-sm font-medium text-white">{r.title}</span>
                  <Badge color={STATUS_COLOR[r.status] ?? 'slate'}>{r.status}</Badge>
                </div>
                <div className="mt-0.5 text-xs text-slate-500">
                  {(r.platform || r.platform_slug || '—')}
                  {r.created_at ? ` · ${r.created_at.split('T')[0]}` : ''}
                  {r.user_id && r.user_id !== 'default' ? ` · ${r.user_id}` : ''}
                </div>
                {r.notes && <div className="mt-0.5 truncate text-xs text-slate-600">{r.notes}</div>}
                {r.admin_notes && <div className="mt-0.5 truncate text-xs text-orange-400/80">{r.admin_notes}</div>}
              </div>
              {canApprove && (
                <Button size="sm" variant="secondary" onClick={() => patchStatus(r, 'approved')} data-testid={`req-approve-${r.id}`}>
                  <Check className="h-3.5 w-3.5" /> Approve
                </Button>
              )}
              <Button size="sm" variant="secondary" onClick={() => runSearch(r)} disabled={searchReq.isPending} data-testid={`req-search-${r.id}`}>
                <Search className="h-3.5 w-3.5" /> Search
              </Button>
              {canCancel && (
                <Button size="sm" variant="ghost" onClick={() => patchStatus(r, 'cancelled')} data-testid={`req-cancel-${r.id}`}>
                  <XCircle className="h-3.5 w-3.5" /> Cancel
                </Button>
              )}
              <Button size="sm" variant="danger" onClick={() => setDeleteTarget(r)} aria-label="Delete request" data-testid={`req-delete-${r.id}`}>
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          )
        })}
      </div>
      {!isLoading && requests.length === 0 && (
        <EmptyState icon={Inbox} title="No requests" hint="Requests from users (or the front door) land here for approval and fulfilment." />
      )}

      <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="New request">
        <form onSubmit={submitCreate} className="space-y-4">
          <Input label="Title" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Game title" data-testid="req-title" />
          <PlatformSelect value={platform} onChange={setPlatform} includeAll={false} testid="req-platform" className="w-full" />
          <Input label="Notes" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Optional notes" data-testid="req-notes" />
          <div className="flex justify-end gap-2 border-t border-slate-800 pt-4">
            <Button type="button" variant="secondary" onClick={() => setCreateOpen(false)}>Cancel</Button>
            <Button type="submit" disabled={create.isPending} data-testid="req-create">Create</Button>
          </div>
        </form>
      </Modal>

      <Modal open={!!picker} onClose={() => setPicker(null)} title={`Results for “${picker?.req.title ?? ''}”`}>
        <div className="max-h-96 space-y-2 overflow-y-auto">
          {picker?.results.map((res, i) => (
            <div key={i} className="flex items-center gap-3 rounded-lg border border-slate-800 bg-slate-800/50 p-3">
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm text-slate-200">{res.title}</div>
                <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-slate-500">
                  {res.indexer && <span>{res.indexer}</span>}
                  {res.size_human && <span>{res.size_human}</span>}
                  {res.source_type && <Badge color={res.source_type === 'ddl' ? 'blue' : 'orange'}>{res.source_type}</Badge>}
                  {res.in_library && <Badge color="emerald">In library</Badge>}
                </div>
              </div>
              <Button size="sm" onClick={() => grab(res)} disabled={downloadReq.isPending} data-testid={`req-result-grab-${i}`}>
                <Download className="h-3.5 w-3.5" /> Grab
              </Button>
            </div>
          ))}
        </div>
      </Modal>

      <ConfirmDialog
        open={!!deleteTarget}
        title="Delete request"
        message={<>Delete the request for <span className="text-slate-200">“{deleteTarget?.title}”</span>? This cannot be undone.</>}
        confirmLabel="Delete"
        danger
        busy={remove.isPending}
        onConfirm={confirmDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  )
}
