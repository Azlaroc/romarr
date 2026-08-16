import { useEffect, useState } from 'react'
import { useConfig, useSaveSetting, useSettings } from '../../api/queries'
import { isForbidden } from '../../api/client'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Card } from '../../components/ui/Card'
import { ConnectionTestTiles } from '../../components/ui/ConnectionTestTiles'
import { Input } from '../../components/ui/Input'
import { ShowAdvancedButton } from '../../components/ui/ShowAdvancedButton'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'
import { useShowAdvanced } from '../../lib/useShowAdvanced'

const TESTABLE_CLIENTS = [
  { id: 'qbittorrent', label: 'qBittorrent' },
  { id: 'sabnzbd', label: 'SABnzbd' },
] as const

export function DownloadClients() {
  const [showAdvanced, setShowAdvanced] = useShowAdvanced()
  return (
    <>
      <PageHeader
        title="Settings"
        subtitle="Download Clients"
        actions={<ShowAdvancedButton show={showAdvanced} onChange={setShowAdvanced} />}
      />
      <div className="space-y-6">
        <Clients />
        <CompletedDownloadHandling showAdvanced={showAdvanced} />
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

function CompletedDownloadHandling({ showAdvanced }: { showAdvanced: boolean }) {
  const { data: settings, error } = useSettings()
  const save = useSaveSetting()
  const { toast } = useToast()
  const [interval, setIntervalStr] = useState('')

  useEffect(() => {
    if (settings?.watcher_interval_seconds !== undefined) setIntervalStr(String(settings.watcher_interval_seconds))
  }, [settings?.watcher_interval_seconds])

  if (isForbidden(error)) {
    return (
      <Card title="Completed download handling">
        <AdminNotice />
      </Card>
    )
  }

  const saveKey = async (patch: Record<string, unknown>) => {
    try {
      await save.mutateAsync(patch)
      toast('Settings saved', 'success')
    } catch {
      toast('Failed to save', 'error')
    }
  }

  const saveInterval = () => {
    const n = Number(interval)
    if (!Number.isInteger(n) || n < 1) {
      toast('Watcher interval must be a whole number of seconds ≥ 1', 'error')
      if (settings?.watcher_interval_seconds !== undefined) setIntervalStr(String(settings.watcher_interval_seconds))
      return
    }
    if (n !== settings?.watcher_interval_seconds) saveKey({ watcher_interval_seconds: n })
  }

  return (
    <Card title="Completed download handling">
      <div className="space-y-3" data-testid="dc-handling">
        <Toggle
          checked={!!settings?.remove_torrent_after_import}
          onChange={(checked) => saveKey({ remove_torrent_after_import: checked })}
          label="Remove torrent after import"
          hint="Delete the torrent from the client once its files are imported"
          data-testid="dc-remove-toggle"
        />
        {showAdvanced && (
          <>
            <Toggle
              checked={!!settings?.seed_janitor_enabled}
              onChange={(checked) => saveKey({ seed_janitor_enabled: checked })}
              label="Seed janitor"
              hint="Remove imported torrents (and files) after their seeding goals complete — deletion is unrecoverable"
              advanced
              data-testid="dc-janitor-toggle"
            />
            <Toggle
              checked={!!settings?.watcher_enabled}
              onChange={(checked) => saveKey({ watcher_enabled: checked })}
              label="Download watcher"
              hint="Polls client queues for completed downloads; disabling stops all torrent imports"
              advanced
              data-testid="dc-watcher-toggle"
            />
            <div className="max-w-xs">
              <Input
                label="Watcher interval (seconds)"
                type="number"
                min={1}
                value={interval}
                onChange={(e) => setIntervalStr(e.target.value)}
                onBlur={saveInterval}
                hint="How often client queues are polled; applies from the next tick"
                advanced
                data-testid="dc-watcher-interval"
              />
            </div>
          </>
        )}
      </div>
    </Card>
  )
}
