import { useState } from 'react'
import { Link } from 'react-router-dom'
import {
  useSources,
  useStats,
  useSettings,
  useSaveSetting,
  useMonitor,
  useTriggerAnalysis,
  useActivity,
  useTestConnection,
} from '../../api/queries'
import { PageHeader } from '../../components/layout/PageHeader'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { useToast } from '../../components/ui/Toast'

const TEST_SERVICES = [
  { id: 'prowlarr', label: 'Prowlarr' },
  { id: 'qbittorrent', label: 'qBittorrent' },
  { id: 'sabnzbd', label: 'SABnzbd' },
  { id: 'nzbget', label: 'NZBGet' },
  { id: 'romm', label: 'RomM' },
]

type TestState = { status: 'idle' | 'testing' | 'ok' | 'fail'; msg?: string }

export function Settings() {
  return (
    <>
      <PageHeader title="Settings" />
      <div className="space-y-6">
        <ConnectionTests />
        <SearchSources />
        <CollectionStats />
        <Options />
        <AiMonitor />
        <RecentActivity />
      </div>
    </>
  )
}

function ConnectionTests() {
  const test = useTestConnection()
  const [results, setResults] = useState<Record<string, TestState>>({})

  const run = async (service: string) => {
    setResults((r) => ({ ...r, [service]: { status: 'testing' } }))
    try {
      const res = await test.mutateAsync(service)
      setResults((r) => ({
        ...r,
        [service]: res.success ? { status: 'ok', msg: 'Connected' } : { status: 'fail', msg: res.error || 'Failed' },
      }))
    } catch (e) {
      setResults((r) => ({ ...r, [service]: { status: 'fail', msg: e instanceof Error ? e.message : 'Error' } }))
    }
  }

  const statusText = (s?: TestState) => {
    if (!s || s.status === 'idle') return <span className="text-slate-500">Not tested</span>
    if (s.status === 'testing') return <span className="text-yellow-400">Testing…</span>
    if (s.status === 'ok') return <span className="text-emerald-400">{s.msg}</span>
    return <span className="text-red-400">{s.msg}</span>
  }

  return (
    <Card title="Connection tests">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {TEST_SERVICES.map((svc) => (
          <button
            key={svc.id}
            onClick={() => run(svc.id)}
            className="rounded-lg border border-slate-700 bg-slate-800 p-3 text-left transition-colors hover:bg-slate-700"
            data-testid={`test-${svc.id}`}
          >
            <div className="text-sm font-medium text-white">{svc.label}</div>
            <div className="mt-1 text-xs" data-testid={`test-${svc.id}-status`}>{statusText(results[svc.id])}</div>
          </button>
        ))}
      </div>
    </Card>
  )
}

function SearchSources() {
  const { data: sources = [] } = useSources()
  return (
    <Card title="Search sources">
      {sources.length === 0 ? (
        <p className="text-sm text-slate-500">No sources configured.</p>
      ) : (
        <div className="space-y-2" data-testid="settings-sources">
          {sources.map((s, i) => (
            <div key={i} className="flex items-center justify-between rounded-lg bg-slate-800 p-3">
              <div className="flex items-center gap-3">
                <span className={`h-2 w-2 rounded-full ${s.enabled ? 'bg-emerald-500' : 'bg-slate-600'}`} />
                <span className="text-sm text-white">{s.label}</span>
              </div>
              <Badge color={s.source_type === 'torrent' ? 'blue' : 'purple'}>{s.source_type}</Badge>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

function CollectionStats() {
  const { data } = useStats()
  const platforms = data?.platforms ?? {}
  const max = Math.max(1, ...Object.values(platforms))
  const sorted = Object.entries(platforms).sort((a, b) => b[1] - a[1])

  return (
    <Card title="Collection stats">
      <div data-testid="settings-stats" className="mb-4 grid grid-cols-2 gap-3">
        <div className="rounded-lg bg-slate-800 p-3 text-center">
          <div className="text-2xl font-bold text-accent-400">{data?.library_total ?? 0}</div>
          <div className="mt-1 text-xs text-slate-500">Library items</div>
        </div>
        <div className="rounded-lg bg-slate-800 p-3 text-center">
          <div className="text-2xl font-bold text-slate-300">{data?.total_jobs ?? 0}</div>
          <div className="mt-1 text-xs text-slate-500">Total jobs</div>
        </div>
      </div>
      {sorted.length === 0 ? (
        <p className="text-sm text-slate-500">No library data yet.</p>
      ) : (
        sorted.map(([slug, count]) => (
          <div key={slug} className="mb-2">
            <div className="mb-0.5 flex justify-between text-xs">
              <span className="text-slate-300">{slug}</span>
              <span className="text-slate-500">{count}</span>
            </div>
            <div className="h-1.5 rounded-full bg-slate-800">
              <div className="h-1.5 rounded-full bg-accent-500" style={{ width: `${Math.round((count / max) * 100)}%` }} />
            </div>
          </div>
        ))
      )}
    </Card>
  )
}

function Options() {
  const { data } = useSettings()
  const save = useSaveSetting()
  const { toast } = useToast()

  const toggle = async (checked: boolean) => {
    try {
      await save.mutateAsync({ extract_archives: checked })
      toast('Settings saved', 'success')
    } catch {
      toast('Failed to save', 'error')
    }
  }

  return (
    <Card title="Options">
      <label className="flex cursor-pointer items-center gap-3">
        <input
          type="checkbox"
          checked={!!data?.extract_archives}
          onChange={(e) => toggle(e.target.checked)}
          className="h-4 w-4 accent-accent-500"
          data-testid="setting-extract"
        />
        <div>
          <div className="text-sm text-white">Extract archives after download</div>
          <div className="text-xs text-slate-500">Auto-extract RAR/ZIP/7z in ROM downloads</div>
        </div>
      </label>
    </Card>
  )
}

function AiMonitor() {
  const { data } = useMonitor()
  const analyze = useTriggerAnalysis()
  const { toast } = useToast()

  return (
    <Card
      title="AI monitor"
      action={
        <Button size="sm" variant="secondary" onClick={() => { analyze.mutate(); toast('Analysis triggered', 'success') }}>
          Analyze now
        </Button>
      }
    >
      <div className="space-y-3" data-testid="monitor-info">
        <div className="flex items-center gap-2">
          <span className={`h-2 w-2 rounded-full ${data?.enabled ? 'bg-emerald-500' : 'bg-slate-600'}`} />
          <span className="text-sm text-slate-300">
            {data?.enabled ? `Active · ${data.provider ?? ''} ${data.model ? `(${data.model})` : ''}` : 'Disabled'}
          </span>
        </div>
        <div className="rounded-lg bg-slate-800 p-3 text-sm text-slate-400">{data?.diagnosis || '—'}</div>
      </div>
    </Card>
  )
}

const EVENT_COLOR: Record<string, string> = {
  download_started: 'text-blue-400',
  download_completed: 'text-emerald-400',
  download_failed: 'text-red-400',
  import_completed: 'text-purple-400',
  download_retried: 'text-yellow-400',
}

function RecentActivity() {
  const { data } = useActivity(1)
  const entries = data?.entries ?? []
  return (
    <Card
      title="Recent activity"
      action={
        <Link to="/activity/history" className="text-xs font-medium text-accent-fg hover:text-accent-300">
          View all →
        </Link>
      }
    >
      {entries.length === 0 ? (
        <p className="text-sm text-slate-500">No activity yet.</p>
      ) : (
        <div className="max-h-64 space-y-1 overflow-y-auto" data-testid="activity-log">
          {entries.slice(0, 25).map((e, i) => (
            <div key={i} className="flex gap-2 border-b border-slate-800/50 py-1.5 text-xs">
              <span className="flex-shrink-0 text-slate-600">{e.timestamp?.split('T')[1]?.slice(0, 5) ?? ''}</span>
              <span className={`flex-shrink-0 font-medium ${EVENT_COLOR[e.event_type] ?? 'text-slate-400'}`}>{e.event_type}</span>
              <span className="truncate text-slate-400">{e.title}</span>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}
