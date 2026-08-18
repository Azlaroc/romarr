import { useRef, useState } from 'react'
import { Database, Upload } from 'lucide-react'
import { PageShell } from '../../components/layout/PageShell'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { DataTable, type Column } from '../../components/ui/DataTable'
import { FormGroup } from '../../components/ui/FormGroup'
import { FormRow } from '../../components/ui/FormRow'
import { InfoPopover } from '../../components/ui/InfoPopover'
import { Input } from '../../components/ui/Input'
import { Modal } from '../../components/ui/Modal'
import { SaveBar, UnsavedChangesPrompt } from '../../components/ui/SaveBar'
import { Select } from '../../components/ui/Select'
import { ShowAdvancedButton } from '../../components/ui/ShowAdvancedButton'
import { Spinner } from '../../components/ui/Spinner'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'
import { isForbidden } from '../../api/client'
import {
  useDatAuthorities,
  useDatCoverage,
  useDatStatus,
  useRefreshDat,
  useSaveSetting,
  useSettings,
  useUpdateDatAuthority,
  useUploadDat,
} from '../../api/queries'
import type { DatAuthority, DatCoverageRow, DatPlatformAssignment, Settings } from '../../api/types'
import { useShowAdvanced } from '../../lib/useShowAdvanced'

const DRIVERS = [
  { value: 'libretro', label: 'libretro mirror' },
  { value: 'redump', label: 'Redump' },
  { value: 'upload', label: 'Hand-fed (upload only)' },
]

type AuthorityEdit = Partial<Pick<DatAuthority, 'enabled' | 'fetch_driver' | 'fetch_base' | 'pinned_version'>>

export function Metadata() {
  const { data, isLoading, error } = useDatAuthorities()
  const { data: status } = useDatStatus()
  const { data: coverage } = useDatCoverage()
  const { data: settings } = useSettings()
  const updateAuthority = useUpdateDatAuthority()
  const refresh = useRefreshDat()
  const upload = useUploadDat()
  const saveSetting = useSaveSetting()
  const { toast } = useToast()
  const [showAdvanced, setShowAdvanced] = useShowAdvanced()

  const [edits, setEdits] = useState<Record<string, AuthorityEdit>>({})
  const [settingEdits, setSettingEdits] = useState<Partial<Settings>>({})
  const [saving, setSaving] = useState(false)
  const [uploadFor, setUploadFor] = useState<DatAuthority | null>(null)

  const authorities = data?.authorities ?? []
  const assignments = data?.platforms ?? []
  const dirty = Object.keys(edits).length > 0 || Object.keys(settingEdits).length > 0

  type StrKey = 'fetch_driver' | 'fetch_base' | 'pinned_version'

  // pinned_version is omitted when unset, so every string field defaults to ''
  // — handing an input undefined would silently make it uncontrolled.
  const strField = (a: DatAuthority, key: StrKey): string => (edits[a.name]?.[key] as string | undefined) ?? a[key] ?? ''
  const boolField = (a: DatAuthority, key: 'enabled'): boolean => edits[a.name]?.[key] ?? a[key]

  const setField = <K extends keyof AuthorityEdit>(a: DatAuthority, key: K, value: AuthorityEdit[K]) => {
    setEdits((prev) => {
      const next = { ...prev, [a.name]: { ...prev[a.name], [key]: value } }
      const merged = next[a.name]
      // Compare against the same '' normalisation, so typing into an unset
      // field and clearing it again leaves the page clean.
      const unchanged = (Object.keys(merged) as (keyof AuthorityEdit)[]).every(
        (k) => (merged[k] ?? '') === (a[k] ?? ''),
      )
      if (unchanged) delete next[a.name]
      return next
    })
  }

  const settingValue = <K extends keyof Settings>(key: K): Settings[K] => settingEdits[key] ?? settings?.[key]

  const setSetting = <K extends keyof Settings>(key: K, value: Settings[K]) => {
    setSettingEdits((prev) => {
      const next = { ...prev, [key]: value }
      if (settings && next[key] === settings[key]) delete next[key]
      return next
    })
  }

  const onSave = async () => {
    setSaving(true)
    const failed: string[] = []
    const warnings: string[] = []
    for (const [name, patch] of Object.entries(edits)) {
      try {
        const res = await updateAuthority.mutateAsync({ name, patch })
        // A driver change can orphan platform assignments. The server reports
        // that rather than blocking, so it has to reach the operator.
        if (res.warnings?.length) warnings.push(...res.warnings)
      } catch {
        failed.push(name)
      }
    }
    if (Object.keys(settingEdits).length) {
      try {
        await saveSetting.mutateAsync(settingEdits)
      } catch {
        failed.push('refresh cadence')
      }
    }
    setSaving(false)
    if (!failed.length) {
      setEdits({})
      setSettingEdits({})
      toast('Metadata settings saved.', 'success')
    } else {
      toast(`Could not save: ${failed.join(', ')}`, 'error')
    }
    for (const w of warnings.slice(0, 4)) toast(w, 'info')
    if (warnings.length > 4) toast(`…and ${warnings.length - 4} more assignment warnings.`, 'info')
  }

  const onRefresh = async (a: DatAuthority) => {
    try {
      const res = await refresh.mutateAsync(a.name)
      // Busy is a 200 with success:false — one writer at a time is normal.
      toast(res.success ? `Refreshing ${a.label}…` : (res.error ?? 'A refresh is already running.'), res.success ? 'success' : 'info')
    } catch (e) {
      toast(e instanceof Error ? e.message : `Could not refresh ${a.label}.`, 'error')
    }
  }

  const canFetch = (a: DatAuthority) => strField(a, 'fetch_driver') !== 'upload' && boolField(a, 'enabled')
  const running = Boolean(status?.running)

  const coverageColumns: Column<DatCoverageRow>[] = [
    { key: 'platform', header: 'Platform', sortValue: (r) => r.platform_slug, render: (r) => r.platform_slug },
    {
      key: 'authority',
      header: 'Authority',
      sortValue: (r) => r.authority ?? '',
      render: (r) => (r.authority ? <Badge color="slate">{r.authority}</Badge> : <span className="text-slate-600">no catalog</span>),
    },
    {
      key: 'counts',
      header: 'Catalog',
      sortValue: (r) => r.known,
      // The server renders this string so the two independent counts can never
      // be presented as a completion figure.
      render: (r) => <span className="tabular-nums text-slate-300">{r.summary}</span>,
    },
    {
      key: 'version',
      header: 'Snapshot',
      sortValue: (r) => r.snapshot_version ?? '',
      render: (r) => <span className="text-xs text-slate-500">{r.snapshot_version || '—'}</span>,
    },
  ]

  if (isForbidden(error)) {
    return (
      <PageShell title="Settings" subtitle="Metadata">
        <AdminNotice />
      </PageShell>
    )
  }

  return (
    <PageShell
      title="Settings"
      subtitle="Metadata"
      tools={<ShowAdvancedButton show={showAdvanced} onChange={setShowAdvanced} />}
    >
      <UnsavedChangesPrompt dirty={dirty} />
      <div className="space-y-6">
        <Card title="Metadata sources">
          <div className="space-y-2" data-testid="md-sources">
            <div className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3">
              <div className="min-w-0">
                <div className="text-sm text-white">DAT authorities</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  The release-layer catalog: every known dump per platform, with names, regions, revisions, sizes and
                  hashes. Configured below.
                </div>
              </div>
              <Badge color="accent">release catalog</Badge>
            </div>
            <div className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3">
              <div className="min-w-0">
                <div className="text-sm text-white">RomM</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  A library peer, not a metadata authority — it owns artwork and details for games already owned, and
                  RomArr notifies it after an import.
                </div>
              </div>
              <Badge color="emerald">library peer</Badge>
            </div>
          </div>
        </Card>

        <FormGroup
          title="DAT authorities"
          description="One authority per platform. The assignment also decides which profile template a platform gets."
          data-testid="md-authorities"
        >
          <div className="space-y-6 pb-2">
            {isLoading && <Spinner label="Loading authorities…" />}
            {authorities.map((a) => {
              const assigned = assignments.filter((p) => p.authority === a.name)
              return (
                <div key={a.name} className="rounded border border-slate-800 p-4" data-testid={`md-authority-${a.name}`}>
                  <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-slate-200">{a.label}</span>
                      <Badge color="slate">{a.kind}</Badge>
                      <span className="text-xs text-slate-500">
                        {assigned.length} {assigned.length === 1 ? 'platform' : 'platforms'}
                      </span>
                    </div>
                    <div className="flex items-center gap-2">
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={!canFetch(a) || running || refresh.isPending}
                        onClick={() => onRefresh(a)}
                        data-testid={`md-refresh-${a.name}`}
                      >
                        {running && status?.authority === a.name ? 'Refreshing…' : 'Refresh now'}
                      </Button>
                      <Button variant="secondary" size="sm" onClick={() => setUploadFor(a)} data-testid={`md-upload-${a.name}`}>
                        <Upload className="h-3.5 w-3.5" /> Upload DAT
                      </Button>
                    </div>
                  </div>

                  <FormRow label="Enabled" hint="A disabled authority is never fetched, and its catalogs stay as they are.">
                    <Toggle
                      checked={boolField(a, 'enabled')}
                      onChange={(v) => setField(a, 'enabled', v)}
                      label="Fetch this authority"
                      data-testid={`md-enabled-${a.name}`}
                    />
                  </FormRow>

                  <FormRow label="Fetch source" hint="Hand-fed authorities are updated by uploading a catalog.">
                    <Select
                      value={strField(a, 'fetch_driver')}
                      onChange={(v) => setField(a, 'fetch_driver', v)}
                      options={DRIVERS}
                      data-testid={`md-driver-${a.name}`}
                    />
                  </FormRow>

                  {showAdvanced && (
                    <>
                      <FormRow label="Fetch base" hint="Where catalogs are downloaded from.">
                        <Input
                          advanced
                          value={strField(a, 'fetch_base')}
                          onChange={(e) => setField(a, 'fetch_base', e.target.value)}
                          placeholder="https://…"
                          data-testid={`md-base-${a.name}`}
                        />
                      </FormRow>
                      <FormRow
                        label="Pinned version"
                        hint="Pinning stops scheduled refreshes from advancing the snapshot. A manual refresh still applies."
                      >
                        <Input
                          advanced
                          value={strField(a, 'pinned_version')}
                          onChange={(e) => setField(a, 'pinned_version', e.target.value)}
                          placeholder="unpinned"
                          data-testid={`md-pin-${a.name}`}
                        />
                      </FormRow>
                    </>
                  )}

                  <div className="pt-3 text-xs text-slate-500" data-testid={`md-status-${a.name}`}>
                    {a.last_refresh ? (
                      <>
                        Last refresh {a.last_refresh}
                        {a.last_status && (
                          <>
                            {' · '}
                            <span className={a.last_status === 'ok' ? 'text-emerald-400' : 'text-red-400'}>{a.last_status}</span>
                          </>
                        )}
                      </>
                    ) : (
                      'Never refreshed'
                    )}
                    {a.last_error && <div className="mt-1 text-red-400">{a.last_error}</div>}
                  </div>
                </div>
              )
            })}
          </div>
        </FormGroup>

        <FormGroup
          title="Refresh cadence"
          description="Advancing a snapshot changes what the library is measured against, so this ships off."
          data-testid="md-cadence"
        >
          <FormRow label="Automatic refresh">
            <Toggle
              checked={Boolean(settingValue('dat_auto_refresh_enabled'))}
              onChange={(v) => setSetting('dat_auto_refresh_enabled', v)}
              label="Refresh catalogs on a schedule"
              data-testid="md-auto-refresh"
            />
          </FormRow>
          <FormRow label="Interval" hint="Days between scheduled refreshes.">
            <div className="max-w-[10rem]">
              <Input
                type="number"
                min={1}
                value={settingValue('dat_refresh_interval_days') ?? 30}
                onChange={(e) => setSetting('dat_refresh_interval_days', Math.max(1, Number(e.target.value) || 1))}
                data-testid="md-refresh-interval"
              />
            </div>
          </FormRow>
        </FormGroup>

        <FormGroup
          title="Catalog coverage"
          description={
            <>
              What is owned and what the catalogs know about, per platform.{' '}
              <InfoPopover label="Catalog coverage">
                These are two independent counts. Owned files have not been matched against catalog entries, so a
                platform can own more files than its catalog lists.
              </InfoPopover>
            </>
          }
          data-testid="md-coverage"
        >
          <div className="pb-3 pt-1">
            <DataTable<DatCoverageRow>
              columns={coverageColumns}
              rows={coverage?.coverage ?? []}
              rowKey={(r) => r.platform_slug}
              initialSort={{ key: 'platform' }}
              testId="md-coverage-table"
              empty={{ icon: Database, title: 'No catalogs imported yet' }}
            />
            {coverage?.note && <p className="mt-4 text-xs text-slate-500">{coverage.note}</p>}
          </div>
        </FormGroup>
      </div>

      <SaveBar dirty={dirty} saving={saving} onSave={onSave} onCancel={() => { setEdits({}); setSettingEdits({}) }} />

      <UploadDialog
        authority={uploadFor}
        assignments={assignments.filter((p) => p.authority === uploadFor?.name)}
        busy={upload.isPending}
        onClose={() => setUploadFor(null)}
        onUpload={async (file, platform) => {
          if (!uploadFor) return
          try {
            const res = await upload.mutateAsync({ name: uploadFor.name, file, platform })
            if (!res.success) {
              toast(res.error ?? 'Upload failed.', 'error')
              return
            }
            const imported = res.imported?.length ?? 0
            const skipped = res.skipped?.length ?? 0
            toast(`Imported ${imported} ${imported === 1 ? 'catalog' : 'catalogs'}${skipped ? `, skipped ${skipped}` : ''}.`, 'success')
            for (const s of (res.skipped ?? []).slice(0, 3)) toast(`Skipped ${s.member}: ${s.reason}`, 'info')
            setUploadFor(null)
          } catch (e) {
            toast(e instanceof Error ? e.message : 'Upload failed.', 'error')
          }
        }}
      />
    </PageShell>
  )
}

function UploadDialog({
  authority,
  assignments,
  busy,
  onClose,
  onUpload,
}: {
  authority: DatAuthority | null
  assignments: DatPlatformAssignment[]
  busy: boolean
  onClose: () => void
  onUpload: (file: File, platform?: string) => void
}) {
  const fileRef = useRef<HTMLInputElement>(null)
  const [platform, setPlatform] = useState('')

  if (!authority) return null

  return (
    <Modal open onClose={onClose} title={`Upload a ${authority.label} catalog`}>
      <div className="space-y-4">
        <p className="text-xs text-slate-500">
          Pick a platform to import one catalog, or leave it on <em>whole pack</em> and upload a zip holding a catalog
          per platform.
        </p>
        <input
          ref={fileRef}
          type="file"
          accept=".dat,.xml,.zip"
          className="block w-full text-sm text-slate-300 file:mr-3 file:rounded file:border-0 file:bg-slate-700 file:px-3 file:py-1.5 file:text-sm file:text-slate-200"
          data-testid="md-upload-file"
        />
        {assignments.length > 0 && (
          <Select
            label="Platform"
            value={platform}
            onChange={setPlatform}
            options={[
              { value: '', label: 'Whole pack (zip)' },
              ...assignments.map((p) => ({ value: p.platform_slug, label: p.platform_slug })),
            ]}
            data-testid="md-upload-platform"
          />
        )}
        <div className="flex justify-end gap-2 border-t border-slate-800 pt-4">
          <Button variant="secondary" size="sm" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            size="sm"
            disabled={busy}
            onClick={() => {
              const file = fileRef.current?.files?.[0]
              if (file) onUpload(file, platform || undefined)
            }}
            data-testid="md-upload-submit"
          >
            {busy ? 'Uploading…' : 'Upload'}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
