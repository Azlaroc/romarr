import { useRef, useState } from 'react'
import { Download } from 'lucide-react'
import { useBackups, useRestoreBackup } from '../../api/queries'
import { isForbidden } from '../../api/client'
import { formatSize } from '../../lib/format'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Card } from '../../components/ui/Card'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { useToast } from '../../components/ui/Toast'

export function Backup() {
  const { data: backups = [], error } = useBackups()
  const restore = useRestoreBackup()
  const { toast } = useToast()
  const fileRef = useRef<HTMLInputElement>(null)
  // Picking a file arms the confirm dialog directly.
  const [pendingFile, setPendingFile] = useState<File | null>(null)

  if (isForbidden(error)) {
    return (
      <>
        <PageHeader title="System" subtitle="Backup" />
        <AdminNotice />
      </>
    )
  }

  const clearPick = () => {
    setPendingFile(null)
    if (fileRef.current) fileRef.current.value = ''
  }

  const confirmRestore = async () => {
    if (!pendingFile) return
    try {
      const res = await restore.mutateAsync(pendingFile)
      toast(`Restored: ${(res.restored ?? []).join(', ') || 'done'} — restart to apply`, 'success')
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Restore failed', 'error')
    } finally {
      clearPick()
    }
  }

  return (
    <>
      <PageHeader title="System" subtitle="Backup" />
      <div className="space-y-6">
        <Card title="Backup">
          <p className="mb-3 text-sm text-slate-400">
            Downloads a ZIP of the database, settings, and DDL sources.
          </p>
          {/* Direct download — the endpoint streams a ZIP with a dated filename. */}
          <a
            href="/api/backup"
            className="inline-flex items-center gap-2 rounded-lg bg-accent-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-accent-500"
            data-testid="backup-download"
          >
            <Download className="h-4 w-4" /> Download backup
          </a>
          {backups.length > 0 && (
            <div className="mt-4 space-y-1 text-xs text-slate-500" data-testid="backup-list">
              <div className="font-medium text-slate-400">Server-side backups</div>
              {backups.map((b) => (
                <div key={b.filename}>
                  {b.name} · {formatSize(b.size)} · {b.created_at.split('T')[0]}
                </div>
              ))}
            </div>
          )}
        </Card>

        <Card title="Restore">
          <p className="mb-3 text-sm text-slate-400">
            Upload a backup ZIP. Only gamarr.db, settings.json, and ddl_sources.json are restored; a restart is
            required afterwards.
          </p>
          <input
            ref={fileRef}
            type="file"
            accept=".zip"
            className="block w-full text-sm text-slate-400 file:mr-3 file:rounded-lg file:border-0 file:bg-slate-800 file:px-4 file:py-2 file:text-sm file:font-medium file:text-slate-200 hover:file:bg-slate-700"
            onChange={(e) => setPendingFile(e.target.files?.[0] ?? null)}
            data-testid="backup-restore"
          />
        </Card>

        <Card title="Export">
          <div className="flex flex-wrap gap-3 text-sm">
            <a href="/api/export/library" className="text-accent-fg hover:text-accent-300" data-testid="export-library">
              Library JSON
            </a>
            <a href="/api/export/wishlist" className="text-accent-fg hover:text-accent-300">
              Wishlist JSON
            </a>
            <a href="/api/export/requests" className="text-accent-fg hover:text-accent-300">
              Requests JSON
            </a>
          </div>
        </Card>
      </div>

      <ConfirmDialog
        open={pendingFile != null}
        title="Restore backup"
        message={`Overwrite the current database and settings with “${pendingFile?.name}”? This cannot be undone.`}
        confirmLabel="Restore"
        danger
        busy={restore.isPending}
        onConfirm={confirmRestore}
        onCancel={clearPick}
      />
    </>
  )
}
