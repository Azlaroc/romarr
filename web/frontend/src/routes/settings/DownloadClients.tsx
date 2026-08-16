import { useConfig, useSettingsEnv } from '../../api/queries'
import { isForbidden } from '../../api/client'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge } from '../../components/ui/Badge'
import { Card } from '../../components/ui/Card'
import { ConnectionTestTiles } from '../../components/ui/ConnectionTestTiles'

const TESTABLE_CLIENTS = [
  { id: 'qbittorrent', label: 'qBittorrent' },
  { id: 'sabnzbd', label: 'SABnzbd' },
  { id: 'nzbget', label: 'NZBGet' },
] as const

const READONLY_CLIENTS = [
  { id: 'transmission', label: 'Transmission' },
  { id: 'deluge', label: 'Deluge' },
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

  const extras = READONLY_CLIENTS.filter((c) => config?.[c.id]?.configured)

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
        {extras.length > 0 && (
          <div className="space-y-2">
            {extras.map((c) => (
              <div key={c.id} className="flex items-center justify-between rounded-lg bg-slate-800 p-3" data-testid={`dc-${c.id}`}>
                <div className="min-w-0">
                  <div className="text-sm text-white">{c.label}</div>
                  <div className="mt-0.5 truncate text-xs text-slate-500">{config?.[c.id]?.url}</div>
                </div>
                <Badge color="slate">configured</Badge>
              </div>
            ))}
          </div>
        )}
      </div>
    </Card>
  )
}

function CompletedDownloadHandling() {
  const { data: env, error } = useSettingsEnv()

  if (isForbidden(error)) {
    return (
      <Card title="Completed download handling">
        <AdminNotice />
      </Card>
    )
  }

  const rows = [
    {
      key: 'remove',
      label: 'Remove torrent after import',
      value: env ? (env.downloads.remove_torrent_after_import ? 'Enabled' : 'Disabled') : '…',
      hint: 'Delete the torrent from the client once its files are imported',
    },
    {
      key: 'janitor',
      label: 'Seed janitor',
      value: env ? (env.downloads.seed_janitor_enabled ? 'Enabled' : 'Disabled') : '…',
      hint: 'Remove imported torrents (and files) after seeding completes',
    },
    {
      key: 'watcher',
      label: 'Download watcher interval',
      value: env ? `${env.downloads.watcher_interval_seconds}s` : '…',
      hint: 'How often client queues are polled for completed downloads',
    },
  ]

  return (
    <Card title="Completed download handling">
      <div className="space-y-2" data-testid="dc-handling">
        {rows.map((r) => (
          <div key={r.key} className="flex items-center justify-between gap-4 rounded-lg bg-slate-800 p-3">
            <div className="min-w-0">
              <div className="text-sm text-white">{r.label}</div>
              <div className="mt-0.5 text-xs text-slate-500">{r.hint}</div>
            </div>
            <span className="shrink-0 text-sm text-slate-300">{r.value}</span>
          </div>
        ))}
        <p className="text-xs text-slate-500">Set by the container environment; requires a restart to change.</p>
      </div>
    </Card>
  )
}
