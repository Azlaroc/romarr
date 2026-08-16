import { useConfig, useSaveSetting, useSettings, useSettingsEnv } from '../../api/queries'
import { isForbidden } from '../../api/client'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Card } from '../../components/ui/Card'
import { ConnectionTestTiles } from '../../components/ui/ConnectionTestTiles'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

const TESTABLE_CLIENTS = [
  { id: 'qbittorrent', label: 'qBittorrent' },
  { id: 'sabnzbd', label: 'SABnzbd' },
] as const

export function DownloadClients() {
  return (
    <>
      <PageHeader title="Settings" subtitle="Download Clients" />
      <div className="space-y-6">
        <Clients />
        <CompletedDownloadHandling />
      </div>
    </>
  )
}

function Clients() {
  const { data: config } = useConfig()

  return (
    <Card title="Download clients">
      <div className="space-y-3" data-testid="dc-clients">
        <p className="text-xs text-slate-500">
          Client endpoints and credentials are set by the container environment; use Test to verify connectivity.
        </p>
        <ConnectionTestTiles
          services={TESTABLE_CLIENTS.map((c) => ({
            id: c.id,
            label: c.label,
            url: config?.[c.id]?.url,
            configured: config?.[c.id]?.configured,
          }))}
        />
      </div>
    </Card>
  )
}

const HANDLING_TOGGLES = [
  {
    key: 'remove_torrent_after_import',
    testId: 'dc-remove-toggle',
    label: 'Remove torrent after import',
    help: 'Delete the torrent from the client once its files are imported',
  },
  {
    key: 'seed_janitor_enabled',
    testId: 'dc-janitor-toggle',
    label: 'Seed janitor',
    help: 'Remove imported torrents (and files) after their seeding goals complete — deletion is unrecoverable',
  },
] as const

function CompletedDownloadHandling() {
  const { data: env, error: envError } = useSettingsEnv()
  const { data: settings, error: settingsError } = useSettings()
  const save = useSaveSetting()
  const { toast } = useToast()

  if (isForbidden(envError) || isForbidden(settingsError)) {
    return (
      <Card title="Completed download handling">
        <AdminNotice />
      </Card>
    )
  }

  const toggle = async (key: string, checked: boolean) => {
    try {
      await save.mutateAsync({ [key]: checked })
      toast('Settings saved', 'success')
    } catch {
      toast('Failed to save', 'error')
    }
  }

  return (
    <Card title="Completed download handling">
      <div className="space-y-3" data-testid="dc-handling">
        {HANDLING_TOGGLES.map((t) => (
          <Toggle
            key={t.key}
            checked={!!settings?.[t.key]}
            onChange={(checked) => toggle(t.key, checked)}
            label={t.label}
            hint={t.help}
            data-testid={t.testId}
          />
        ))}
        <div className="flex items-center justify-between gap-4 rounded bg-slate-800 p-3">
          <div className="min-w-0">
            <div className="text-sm text-white">Download watcher interval</div>
            <div className="mt-0.5 text-xs text-slate-500">How often client queues are polled for completed downloads</div>
          </div>
          <span className="shrink-0 text-sm text-slate-300">{env ? `${env.downloads.watcher_interval_seconds}s` : '…'}</span>
        </div>
        <p className="text-xs text-slate-500">Watcher interval is set by the container environment; requires a restart to change.</p>
      </div>
    </Card>
  )
}
