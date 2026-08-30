import { useState } from 'react'
import {
  useAddWebhook,
  useConfig,
  useDeleteWebhook,
  useSaveSetting,
  useSettings,
  useTestWebhook,
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
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

const WEBHOOK_TYPES = [
  { value: 'generic', label: 'Generic JSON' },
  { value: 'discord', label: 'Discord' },
]

const EVENT_HINT =
  'Comma-separated events, or * for all: download_complete, download_failed, scheduler_match'

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
          Notify external services when things happen — downloads completing or failing, the scheduler matching
          their lifecycle, scheduler matches.
        </p>
        <div className="space-y-2" data-testid="cn-webhook-list">
          {hooks.map((h) => (
            <div key={h.id} className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3" data-testid={`cn-webhook-${h.id}`}>
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
  const { data: settings, error: settingsError } = useSettings()
  const save = useSaveSetting()
  const { toast } = useToast()

  const saveKey = async (patch: Record<string, unknown>) => {
    try {
      await save.mutateAsync(patch)
      toast('Settings saved', 'success')
    } catch {
      toast('Failed to save', 'error')
    }
  }

  return (
    <Card title="RomM">
      <div className="space-y-3" data-testid="cn-romm">
        <p className="text-xs text-slate-500">
          RomM is a library peer serving its own consumers; imports notify it to rescan the changed platform.
          What this app holds is owned by its own scanner (Library → Scan), not synced from RomM.
        </p>
        <ConnectionTestTiles
          services={[{ id: 'romm', label: 'RomM', url: config?.romm?.url ?? config?.romm_url, configured: config?.romm?.configured }]}
        />
        {isForbidden(settingsError) ? (
          <AdminNotice />
        ) : (
          <Toggle
            checked={!!settings?.romm_connect_enabled}
            onChange={(checked) => saveKey({ romm_connect_enabled: checked })}
            label="Import scan notifications"
            hint="Ask RomM to rescan a platform right after an import lands (needs RomM API credentials with tasks.run)"
            data-testid="cn-connect-toggle"
          />
        )}
      </div>
    </Card>
  )
}
