import { useEffect, useState } from 'react'
import {
  useAddDDLSource,
  useConfig,
  useDDLSources,
  useDeleteDDLSource,
  useResetSource,
  useSaveSetting,
  useSettings,
  useSourceRegistry,
  useSources,
  useSourcesHealth,
  useUpdateSourceSpec,
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
import { Modal } from '../../components/ui/Modal'
import { Select } from '../../components/ui/Select'
import { ShowAdvancedButton } from '../../components/ui/ShowAdvancedButton'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'
import { useShowAdvanced } from '../../lib/useShowAdvanced'

export function Indexers() {
  const [showAdvanced, setShowAdvanced] = useShowAdvanced()
  return (
    <>
      <PageHeader
        title="Settings"
        subtitle="Indexers"
        actions={<ShowAdvancedButton show={showAdvanced} onChange={setShowAdvanced} />}
      />
      <div className="space-y-6">
        <SearchSources />
        <SourceRegistryCard />
        <WishlistSearch showAdvanced={showAdvanced} />
        <DDLSources />
        <ProwlarrCard />
      </div>
    </>
  )
}

// Wishlist Search: the scheduler's search/grab knobs — the RSS-sync analog,
// which the arrs keep under Settings › Indexers.
function WishlistSearch({ showAdvanced }: { showAdvanced: boolean }) {
  const { data: settings, error } = useSettings()
  const save = useSaveSetting()
  const { toast } = useToast()
  const [minScore, setMinScore] = useState('')
  const [intervalHrs, setIntervalHrs] = useState('')
  const [setTimeout_, setSetTimeout] = useState('')

  // Sync local inputs once settings arrive (no dirty tracking: saves on blur).
  useEffect(() => {
    if (settings?.scheduler_min_score !== undefined) setMinScore(String(settings.scheduler_min_score))
  }, [settings?.scheduler_min_score])
  useEffect(() => {
    if (settings?.scheduler_interval_hours !== undefined) setIntervalHrs(String(settings.scheduler_interval_hours))
  }, [settings?.scheduler_interval_hours])
  useEffect(() => {
    if (settings?.selector_set_timeout_hours !== undefined) setSetTimeout(String(settings.selector_set_timeout_hours))
  }, [settings?.selector_set_timeout_hours])

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

  const saveInt = (key: string, raw: string, min: number, current: number | undefined, reset: (v: string) => void, what: string) => {
    const n = Number(raw)
    if (!Number.isInteger(n) || n < min) {
      toast(`${what} must be a whole number ≥ ${min}`, 'error')
      if (current !== undefined) reset(String(current))
      return
    }
    if (n !== current) saveKey({ [key]: n })
  }

  return (
    <Card title="Wishlist search">
      <div className="space-y-3" data-testid="idx-wishlist-search">
        <Toggle
          checked={!!settings?.scheduler_enabled}
          onChange={(checked) => saveKey({ scheduler_enabled: checked })}
          label="Scheduled wishlist search"
          hint="Periodically search every wishlist entry; disabling stops automatic cycles (manual Run Now keeps working)"
          data-testid="idx-scheduler-toggle"
        />
        <Toggle
          checked={!!settings?.scheduler_auto_download}
          onChange={(checked) => saveKey({ scheduler_auto_download: checked })}
          label="Automatic download"
          hint="Let scheduled wishlist searches grab the best candidate; off = search-only cycles"
          data-testid="idx-autodl-toggle"
        />
        <div className="max-w-xs">
          <Input
            label="Search interval (hours)"
            type="number"
            min={1}
            value={intervalHrs}
            onChange={(e) => setIntervalHrs(e.target.value)}
            onBlur={() => saveInt('scheduler_interval_hours', intervalHrs, 1, settings?.scheduler_interval_hours, setIntervalHrs, 'Search interval')}
            hint="Hours between automatic wishlist cycles"
            data-testid="idx-interval-input"
          />
        </div>
        {showAdvanced && (
          <>
            <div className="max-w-xs">
              <Input
                label="Minimum score"
                type="number"
                min={1}
                value={minScore}
                onChange={(e) => setMinScore(e.target.value)}
                onBlur={() => saveInt('scheduler_min_score', minScore, 1, settings?.scheduler_min_score, setMinScore, 'Minimum score')}
                hint="Candidates scoring below this are never auto-grabbed (1–100)"
                advanced
                data-testid="idx-minscore-input"
              />
            </div>
            <div className="max-w-xs">
              <Select
                label="Selector mode"
                value={settings?.selector_mode ?? 'shadow'}
                onChange={(v) => saveKey({ selector_mode: v })}
                options={[
                  { value: 'off', label: 'Off — legacy top-score grabs' },
                  { value: 'shadow', label: 'Shadow — selector logs, legacy grabs' },
                  { value: 'enforce', label: 'Enforce — selector drives grabs' },
                ]}
                advanced
                data-testid="idx-selector-mode"
              />
              <p className="mt-1 text-xs text-slate-600">
                Mode changes apply at the next cycle. Switching off↔enforce mid-flight changes when wishlist rows are
                consumed — avoid flipping while a cycle is running.
              </p>
            </div>
            <div className="max-w-xs">
              <Input
                label="Disc-set timeout (hours)"
                type="number"
                min={1}
                value={setTimeout_}
                onChange={(e) => setSetTimeout(e.target.value)}
                onBlur={() => saveInt('selector_set_timeout_hours', setTimeout_, 1, settings?.selector_set_timeout_hours, setSetTimeout, 'Disc-set timeout')}
                hint="How long a multi-disc set may wait for stragglers before degrading to the discs on hand"
                advanced
                data-testid="idx-settimeout-input"
              />
            </div>
          </>
        )}
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

// SourceRegistryCard edits the built-in driver specs (Vimm, Internet
// Archive): enabled flag, base URL, and the platform→item/system mapping.
// Edits hot-apply — the backend swaps the live registry on save.
function SourceRegistryCard() {
  const { data: rows = [], error } = useSourceRegistry()
  const update = useUpdateSourceSpec()
  const { toast } = useToast()
  const [editing, setEditing] = useState<string | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [baseUrl, setBaseUrl] = useState('')
  const [mappingText, setMappingText] = useState('')

  if (isForbidden(error)) {
    return (
      <Card title="Sources">
        <AdminNotice />
      </Card>
    )
  }

  const openEditor = (name: string) => {
    const row = rows.find((r) => r.name === name)
    if (!row) return
    setEnabled(row.enabled)
    setBaseUrl(row.base_url)
    setMappingText(JSON.stringify(row.mapping ?? {}, null, 2))
    setEditing(name)
  }

  const saveEditor = async () => {
    if (!editing) return
    let mapping: Record<string, string>
    try {
      mapping = JSON.parse(mappingText)
      if (typeof mapping !== 'object' || mapping === null || Array.isArray(mapping)) throw new Error('not an object')
      for (const v of Object.values(mapping)) {
        if (typeof v !== 'string') throw new Error('values must be strings')
      }
    } catch {
      toast('Mapping must be a JSON object of platform → identifier strings', 'error')
      return
    }
    try {
      await update.mutateAsync({ name: editing, patch: { enabled, base_url: baseUrl, mapping } })
      toast('Source saved', 'success')
      setEditing(null)
    } catch {
      toast('Failed to save source', 'error')
    }
  }

  const editingRow = rows.find((r) => r.name === editing)

  return (
    <Card title="Sources">
      <div className="space-y-2" data-testid="idx-src-list">
        <p className="text-xs text-slate-500">
          Built-in search drivers. Edits apply immediately — no restart. A source is active only when enabled AND it
          has platforms mapped.
        </p>
        {rows.map((r) => (
          <div key={r.name} className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3" data-testid={`idx-src-row-${r.name}`}>
            <div className="flex min-w-0 items-center gap-3">
              <span className={`h-2 w-2 shrink-0 rounded-full ${r.active ? 'bg-emerald-500' : 'bg-slate-600'}`} />
              <div className="min-w-0">
                <div className="text-sm text-white">{r.label}</div>
                <div className="mt-0.5 truncate text-xs text-slate-500">
                  {r.base_url || 'no base URL'} · {Object.keys(r.mapping ?? {}).length} platforms mapped
                  {!r.enabled && ' · disabled'}
                </div>
              </div>
            </div>
            <Button size="sm" variant="secondary" onClick={() => openEditor(r.name)} data-testid={`idx-src-edit-${r.name}`}>
              Edit
            </Button>
          </div>
        ))}
      </div>

      <Modal open={editing !== null} onClose={() => setEditing(null)} title={`Edit ${editingRow?.label ?? ''}`}>
        <div className="space-y-4 p-4">
          <Toggle
            checked={enabled}
            onChange={setEnabled}
            label="Enabled"
            hint="Disabled sources are skipped by every search"
            data-testid="idx-src-enabled"
          />
          <Input label="Base URL" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} data-testid="idx-src-baseurl" />
          <div>
            <span className="mb-1 block text-xs font-medium text-slate-400">
              Platform mapping (JSON: platform slug → {editingRow?.name === 'archiveorg' ? 'archive.org item' : 'site system id'})
            </span>
            <textarea
              value={mappingText}
              onChange={(e) => setMappingText(e.target.value)}
              rows={8}
              className="w-full rounded border border-slate-700 bg-slate-800 px-3 py-2 font-mono text-xs text-slate-300 focus:border-accent-500 focus:outline-none"
              data-testid="idx-src-mapping"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="secondary" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button size="sm" onClick={saveEditor} disabled={update.isPending} data-testid="idx-src-save">
              Save
            </Button>
          </div>
        </div>
      </Modal>
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
  const [deleting, setDeleting] = useState<{ id: number; name: string } | null>(null)

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
      await del.mutateAsync(deleting.id)
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
        <p className="text-xs text-slate-500">
          Custom rows are placeholders for future direct-download drivers — search does not consume them yet.
        </p>
        <div className="space-y-2" data-testid="idx-ddl-list">
          {rows.map((r, i) => (
            <div key={r.id ?? `builtin-${i}`} className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3" data-testid={`idx-ddl-${i}`}>
              <div className="min-w-0">
                <div className="text-sm text-white">{r.name}</div>
                <div className="mt-0.5 truncate text-xs text-slate-500">{r.url}</div>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <Badge color={r.builtin ? 'slate' : 'accent'}>{r.builtin ? 'built-in' : r.type}</Badge>
                {!r.builtin && r.id !== undefined && (
                  <Button
                    size="sm"
                    variant="danger"
                    onClick={() => setDeleting({ id: r.id!, name: r.name })}
                    data-testid={`idx-ddl-delete-${r.id}`}
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
