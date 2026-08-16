import { useState } from 'react'
import {
  useAddWebhook,
  useConfig,
  useDeleteWebhook,
  useSettingsEnv,
  useSyncStatus,
  useTestWebhook,
  useTriggerSync,
  useWebhooks,
} from '../../api/queries'
import { isForbidden } from '../../api/client'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { ConnectionTestTiles } from '../../components/ui/ConnectionTestTiles'
import { Input } from '../../components/ui/Input'
import { Select } from '../../components/ui/Select'
import { useToast } from '../../components/ui/Toast'

const WEBHOOK_TYPES = [
  { value: 'generic', label: 'Generic JSON' },
  { value: 'discord', label: 'Discord' },
]

const EVENT_HINT =
  'Comma-separated events, or * for all: download_complete, download_failed, request_created, request_approved, request_completed, request_failed, scheduler_match'

export function Connect() {
  return (
    <>
      <PageHeader title="Settings" subtitle="Connect" />
      <div className="space-y-6">
        <Webhooks />
        <RomM />
      </div>
    </>
  )
}

function Webhooks() {
  const { data: hooks = [] } = useWebhooks()
  const add = useAddWebhook()
  const del = useDeleteWebhook()
  const test = useTestWebhook()
  const { toast } = useToast()

  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [type, setType] = useState('generic')
  const [events, setEvents] = useState('*')
  const [deleting, setDeleting] = useState<{ id: number; name: string } | null>(null)

  const submit = async () => {
    try {
      await add.mutateAsync({ name: name.trim(), url: url.trim(), type, events: events.trim() || '*' })
      toast('Webhook added', 'success')
      setName('')
      setUrl('')
      setEvents('*')
    } catch {
      toast('Failed to add webhook', 'error')
    }
  }

  const runTest = async (id: number) => {
    try {
      const res = await test.mutateAsync({ id })
      if (res?.success) toast('Test notification sent', 'success')
      else toast(res?.error || 'Test failed', 'error')
    } catch {
      toast('Test failed', 'error')
    }
  }

  const confirmDelete = async () => {
    if (!deleting) return
    try {
      await del.mutateAsync(deleting.id)
      toast('Webhook removed', 'success')
    } catch {
      toast('Failed to remove webhook', 'error')
    } finally {
      setDeleting(null)
    }
  }

  return (
    <Card title="Webhooks">
      <div className="space-y-3">
        <p className="text-xs text-slate-500">
          Notify external services when things happen — downloads completing or failing, requests moving through
          their lifecycle, scheduler matches.
        </p>
        <div className="space-y-2" data-testid="cn-webhook-list">
          {hooks.map((h) => (
            <div key={h.id} className="flex items-center justify-between gap-3 rounded-lg bg-slate-800 p-3" data-testid={`cn-webhook-${h.id}`}>
              <div className="min-w-0">
                <div className="flex items-center gap-3">
                  <span className={`h-2 w-2 shrink-0 rounded-full ${h.enabled ? 'bg-emerald-500' : 'bg-slate-600'}`} />
                  <span className="text-sm text-white">{h.name}</span>
                </div>
                <div className="mt-0.5 truncate pl-5 text-xs text-slate-500">
                  {h.url}
                  {h.events && h.events !== '*' ? ` · ${h.events}` : ' · all events'}
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <Badge color={h.type === 'discord' ? 'purple' : 'slate'}>{h.type || 'generic'}</Badge>
                <Button size="sm" variant="ghost" onClick={() => runTest(h.id)} disabled={test.isPending} data-testid={`cn-webhook-test-${h.id}`}>
                  Test
                </Button>
                <Button size="sm" variant="danger" onClick={() => setDeleting({ id: h.id, name: h.name })} data-testid={`cn-webhook-delete-${h.id}`}>
                  Delete
                </Button>
              </div>
            </div>
          ))}
        </div>
        <div className="flex flex-wrap items-end gap-2">
          <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} placeholder="My webhook" className="max-w-40" data-testid="cn-webhook-name" />
          <Input label="URL" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…" className="max-w-64" data-testid="cn-webhook-url" />
          <Select label="Type" value={type} onChange={setType} options={WEBHOOK_TYPES} className="max-w-36" />
          <Input label="Events" value={events} onChange={(e) => setEvents(e.target.value)} hint={EVENT_HINT} className="max-w-48" data-testid="cn-webhook-events" />
          <Button size="sm" onClick={submit} disabled={add.isPending || !name.trim() || !url.trim()} data-testid="cn-webhook-add">
            Add webhook
          </Button>
        </div>
      </div>

      <ConfirmDialog
        open={deleting !== null}
        title="Remove webhook"
        message={<p>Remove “{deleting?.name}”? It stops receiving notifications immediately.</p>}
        confirmLabel="Remove"
        danger
        busy={del.isPending}
        onConfirm={confirmDelete}
        onCancel={() => setDeleting(null)}
      />
    </Card>
  )
}

function RomM() {
  const { data: config } = useConfig()
  const { data: env, error: envError } = useSettingsEnv()
  const { data: sync } = useSyncStatus()
  const trigger = useTriggerSync()
  const { toast } = useToast()

  const syncNow = async () => {
    try {
      const res = await trigger.mutateAsync(false)
      if (res?.success) toast('RomM library sync started', 'success')
      else toast(res?.error || 'Sync failed to start', 'error')
    } catch {
      toast('Sync failed to start', 'error')
    }
  }

  const syncLine = () => {
    if (!sync) return '…'
    if (!sync.enabled) return 'Sync disabled'
    if (sync.running) return 'Sync running…'
    const last = (sync.last_run ?? sync.last_sync) as string | undefined
    return last ? `Idle · last run ${last}` : 'Idle · never run'
  }

  return (
    <Card title="RomM">
      <div className="space-y-3" data-testid="cn-romm">
        <p className="text-xs text-slate-500">
          RomM is the library of record: ownership syncs from it, and imports notify it to rescan the changed
          platform.
        </p>
        <ConnectionTestTiles
          services={[{ id: 'romm', label: 'RomM', url: config?.romm?.url ?? config?.romm_url, configured: config?.romm?.configured }]}
        />
        {isForbidden(envError) ? (
          <AdminNotice />
        ) : (
          <div className="space-y-2">
            <div className="flex items-center justify-between rounded-lg bg-slate-800 p-3">
              <div className="min-w-0">
                <div className="text-sm text-white">Import scan notifications</div>
                <div className="mt-0.5 text-xs text-slate-500">Ask RomM to rescan a platform right after an import lands</div>
              </div>
              <span className="shrink-0 text-sm text-slate-300">{env ? (env.romm.connect_enabled ? 'Enabled' : 'Disabled') : '…'}</span>
            </div>
            <div className="flex items-center justify-between rounded-lg bg-slate-800 p-3">
              <div className="min-w-0">
                <div className="text-sm text-white">Ownership sync</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  Periodic pull of RomM's library into the local ownership index
                  {env ? ` · every ${env.romm.sync_interval_seconds}s` : ''}
                </div>
              </div>
              <span className="shrink-0 text-sm text-slate-300">{env ? (env.romm.sync_enabled ? 'Enabled' : 'Disabled') : '…'}</span>
            </div>
            <p className="text-xs text-slate-500">Set by the container environment; requires a restart to change.</p>
          </div>
        )}
        <div className="flex items-center gap-3">
          <Button size="sm" variant="secondary" onClick={syncNow} disabled={trigger.isPending || sync?.running === true} data-testid="cn-sync-now">
            Sync now
          </Button>
          <span className="text-xs text-slate-500" data-testid="cn-sync-status">{syncLine()}</span>
        </div>
      </div>
    </Card>
  )
}
