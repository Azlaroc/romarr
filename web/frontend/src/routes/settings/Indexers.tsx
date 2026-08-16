import { useEffect, useState } from 'react'
import {
  useAddDDLSource,
  useConfig,
  useDDLSources,
  useDeleteDDLSource,
  useResetSource,
  useSaveSetting,
  useSettings,
  useSources,
  useSourcesHealth,
} from '../../api/queries'
import type { SourceHealth } from '../../api/types'
import { isForbidden } from '../../api/client'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { ConnectionTestTiles } from '../../components/ui/ConnectionTestTiles'
import { Input } from '../../components/ui/Input'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

export function Indexers() {
  return (
    <>
      <PageHeader title="Settings" subtitle="Indexers" />
      <div className="space-y-6">
        <SearchSources />
        <WishlistSearch />
        <DDLSources />
        <ProwlarrCard />
      </div>
    </>
  )
}

// Wishlist Search: the scheduler's grab knobs — the RSS-sync analog, which
// the arrs keep under Settings › Indexers.
function WishlistSearch() {
  const { data: settings, error } = useSettings()
  const save = useSaveSetting()
  const { toast } = useToast()
  const [minScore, setMinScore] = useState('')

  // Sync the local input once settings arrive (no dirty tracking: saves on blur).
  useEffect(() => {
    if (settings?.scheduler_min_score !== undefined) setMinScore(String(settings.scheduler_min_score))
  }, [settings?.scheduler_min_score])

  if (isForbidden(error)) {
    return (
      <Card title="Wishlist search">
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

  const saveMinScore = () => {
    const n = Number(minScore)
    if (!Number.isInteger(n) || n < 1) {
      toast('Minimum score must be a whole number ≥ 1', 'error')
      setMinScore(settings?.scheduler_min_score !== undefined ? String(settings.scheduler_min_score) : '')
      return
    }
    if (n !== settings?.scheduler_min_score) saveKey({ scheduler_min_score: n })
  }

  return (
    <Card title="Wishlist search">
      <div className="space-y-3" data-testid="idx-wishlist-search">
        <Toggle
          checked={!!settings?.scheduler_auto_download}
          onChange={(checked) => saveKey({ scheduler_auto_download: checked })}
          label="Automatic download"
          hint="Let scheduled wishlist searches grab the best candidate; off = search-only cycles"
          data-testid="idx-autodl-toggle"
        />
        <div className="max-w-xs">
          <Input
            label="Minimum score"
            type="number"
            min={1}
            value={minScore}
            onChange={(e) => setMinScore(e.target.value)}
            onBlur={saveMinScore}
            hint="Candidates scoring below this are never auto-grabbed (1–100)"
            data-testid="idx-minscore-input"
          />
        </div>
      </div>
    </Card>
  )
}

function healthLine(h: SourceHealth): string {
  if (h.circuit_open) return `Circuit open — retry in ${h.circuit_retry_in_sec}s`
  const parts = [`score ${h.score}`]
  if (h.search_ok + h.search_fail > 0) parts.push(`search ${h.search_ok}/${h.search_ok + h.search_fail}`)
  if (h.download_ok + h.download_fail > 0) parts.push(`dl ${h.download_ok}/${h.download_ok + h.download_fail}`)
  if (h.last_error) parts.push(h.last_error)
  return parts.join(' · ')
}

function SearchSources() {
  const { data: sources = [] } = useSources()
  const { data: health = {} } = useSourcesHealth()
  const reset = useResetSource()
  const { toast } = useToast()

  const doReset = async (name: string) => {
    try {
      const res = await reset.mutateAsync(name)
      if (res?.success) toast('Circuit reset', 'success')
      else toast(res?.error || 'Reset failed', 'error')
    } catch {
      toast('Reset failed', 'error')
    }
  }

  // Health entries with no /api/sources row (e.g. registry-driven DDL drivers)
  // still deserve a row — they are real sources with real breaker state.
  const known = new Set(sources.map((s) => s.name).filter(Boolean))
  const extras = Object.entries(health).filter(([name]) => !known.has(name))

  return (
    <Card title="Search sources">
      <div className="space-y-2" data-testid="settings-sources">
        {sources.length === 0 && extras.length === 0 && (
          <p className="text-sm text-slate-500">No sources configured.</p>
        )}
        {sources.map((s, i) => {
          const h = s.name ? (health[s.name] ?? s.health) : s.health
          return (
            <div key={s.name ?? i} className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3">
              <div className="min-w-0">
                <div className="flex items-center gap-3">
                  <span className={`h-2 w-2 shrink-0 rounded-full ${s.enabled ? 'bg-emerald-500' : 'bg-slate-600'}`} />
                  <span className="text-sm text-white">{s.label}</span>
                </div>
                {h && (
                  <div className={`mt-0.5 pl-5 text-xs ${h.circuit_open ? 'text-red-400' : 'text-slate-500'}`}>
                    {healthLine(h)}
                  </div>
                )}
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <Badge color={s.source_type === 'torrent' ? 'blue' : 'purple'}>{s.source_type}</Badge>
                {h && s.name && (
                  <Button size="sm" variant="ghost" onClick={() => doReset(s.name!)} data-testid={`idx-reset-${s.name}`}>
                    Reset
                  </Button>
                )}
              </div>
            </div>
          )
        })}
        {extras.map(([name, h]) => (
          <div key={name} className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3">
            <div className="min-w-0">
              <div className="flex items-center gap-3">
                <span className={`h-2 w-2 shrink-0 rounded-full ${h.circuit_open ? 'bg-red-500' : 'bg-emerald-500'}`} />
                <span className="text-sm text-white">{h.name || name}</span>
              </div>
              <div className={`mt-0.5 pl-5 text-xs ${h.circuit_open ? 'text-red-400' : 'text-slate-500'}`}>
                {healthLine(h)}
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <Badge color="purple">ddl</Badge>
              <Button size="sm" variant="ghost" onClick={() => doReset(name)} data-testid={`idx-reset-${name}`}>
                Reset
              </Button>
            </div>
          </div>
        ))}
      </div>
    </Card>
  )
}

function DDLSources() {
  const { data: rows = [] } = useDDLSources()
  const add = useAddDDLSource()
  const del = useDeleteDDLSource()
  const { toast } = useToast()

  const [name, setName] = useState('')
  const [url, setUrl] = useState('')
  const [deleting, setDeleting] = useState<{ customIdx: number; name: string } | null>(null)

  const builtinCount = rows.filter((r) => r.builtin).length

  const submit = async () => {
    try {
      await add.mutateAsync({ name: name.trim(), url: url.trim() })
      toast('Source added', 'success')
      setName('')
      setUrl('')
    } catch {
      toast('Failed to add source', 'error')
    }
  }

  const confirmDelete = async () => {
    if (!deleting) return
    try {
      await del.mutateAsync(deleting.customIdx)
      toast('Source removed', 'success')
    } catch {
      toast('Failed to remove source', 'error')
    } finally {
      setDeleting(null)
    }
  }

  return (
    <Card title="DDL sources">
      <div className="space-y-3">
        <div className="space-y-2" data-testid="idx-ddl-list">
          {rows.map((r, i) => (
            <div key={`${r.name}-${i}`} className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3" data-testid={`idx-ddl-${i}`}>
              <div className="min-w-0">
                <div className="text-sm text-white">{r.name}</div>
                <div className="mt-0.5 truncate text-xs text-slate-500">{r.url}</div>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <Badge color={r.builtin ? 'slate' : 'accent'}>{r.builtin ? 'built-in' : r.type}</Badge>
                {!r.builtin && (
                  <Button
                    size="sm"
                    variant="danger"
                    onClick={() => setDeleting({ customIdx: i - builtinCount, name: r.name })}
                    data-testid={`idx-ddl-delete-${i}`}
                  >
                    Delete
                  </Button>
                )}
              </div>
            </div>
          ))}
        </div>
        <div className="flex flex-wrap items-end gap-2">
          <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} placeholder="My source" className="max-w-48" data-testid="idx-ddl-name" />
          <Input label="URL" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…" className="max-w-72" data-testid="idx-ddl-url" />
          <Button size="sm" onClick={submit} disabled={add.isPending || !name.trim() || !url.trim()} data-testid="idx-ddl-add">
            Add source
          </Button>
        </div>
      </div>

      <ConfirmDialog
        open={deleting !== null}
        title="Remove DDL source"
        message={<p>Remove “{deleting?.name}”? Search stops using it immediately.</p>}
        confirmLabel="Remove"
        danger
        busy={del.isPending}
        onConfirm={confirmDelete}
        onCancel={() => setDeleting(null)}
      />
    </Card>
  )
}

function ProwlarrCard() {
  const { data: config } = useConfig()
  return (
    <Card title="Prowlarr / Torznab">
      <div className="space-y-3">
        <p className="text-xs text-slate-500">
          Torrent indexers are managed in Prowlarr; results arrive via its Torznab feeds. The endpoint is set by the
          container environment.
        </p>
        <ConnectionTestTiles
          services={[{ id: 'prowlarr', label: 'Prowlarr', url: config?.prowlarr?.url, configured: config?.prowlarr?.configured }]}
        />
      </div>
    </Card>
  )
}
