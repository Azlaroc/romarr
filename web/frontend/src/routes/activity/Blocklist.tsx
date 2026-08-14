import { useState } from 'react'
import { Ban, Trash2 } from 'lucide-react'
import { useBlocklist, useClearBlocklist, useDeleteBlocklistEntry } from '../../api/queries'
import { PageHeader } from '../../components/layout/PageHeader'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { EmptyState } from '../../components/ui/EmptyState'
import { useToast } from '../../components/ui/Toast'

export function Blocklist() {
  const { data } = useBlocklist()
  const del = useDeleteBlocklistEntry()
  const clear = useClearBlocklist()
  const { toast } = useToast()
  const [confirmClear, setConfirmClear] = useState(false)

  const items = data?.items ?? []
  const total = data?.total ?? 0

  const clearAll = async () => {
    try {
      const res = await clear.mutateAsync()
      toast(`Cleared ${res?.deleted ?? 0} entries`, 'success')
    } catch {
      toast('Failed to clear blocklist', 'error')
    }
    setConfirmClear(false)
  }

  return (
    <>
      <PageHeader
        title="Activity"
        subtitle="Blocklist"
        actions={
          <Button variant="secondary" size="sm" onClick={() => setConfirmClear(true)} disabled={total === 0} data-testid="bl-clear">
            <Trash2 className="h-3.5 w-3.5" /> Clear all
          </Button>
        }
      />

      {/* Container stays mounted when empty so tests can observe rows appearing/shrinking. */}
      <div className="space-y-2" data-testid="blocklist-table">
        {items.map((b) => (
          <div key={b.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-900 p-4" data-testid={`bl-row-${b.id}`}>
            <Ban className="h-4 w-4 flex-shrink-0 text-slate-600" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="truncate text-sm font-medium text-white">{b.title || b.download_url || b.info_hash}</span>
                {b.source && <Badge>{b.source}</Badge>}
              </div>
              <div className="mt-0.5 text-xs text-slate-500">
                {b.reason || 'No reason recorded'}
                {/* created_at is SQLite datetime text, not RFC3339 — render the substring verbatim. */}
                {b.created_at ? ` · ${b.created_at.slice(0, 16)}` : ''}
              </div>
            </div>
            <Button size="sm" variant="danger" onClick={() => del.mutate(b.id)} aria-label="Remove from blocklist" data-testid={`bl-remove-${b.id}`}>
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        ))}
      </div>
      {items.length === 0 && (
        <EmptyState icon={Ban} title="Blocklist is empty" hint="Failed or rejected releases land here so they are never grabbed again." />
      )}

      <ConfirmDialog
        open={confirmClear}
        title="Clear blocklist"
        message={<>Remove all {total} blocklist entries?</>}
        confirmLabel="Clear all"
        danger
        busy={clear.isPending}
        onConfirm={clearAll}
        onCancel={() => setConfirmClear(false)}
      />
    </>
  )
}
