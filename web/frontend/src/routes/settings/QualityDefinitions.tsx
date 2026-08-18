import { useMemo, useState } from 'react'
import { Ruler } from 'lucide-react'
import { PageShell } from '../../components/layout/PageShell'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { DataTable, type Column } from '../../components/ui/DataTable'
import { FormGroup } from '../../components/ui/FormGroup'
import { InfoPopover } from '../../components/ui/InfoPopover'
import { SaveBar, UnsavedChangesPrompt } from '../../components/ui/SaveBar'
import { inputCls } from '../../components/ui/Input'
import { useToast } from '../../components/ui/Toast'
import { isForbidden } from '../../api/client'
import { useResetSizeDefinition, useSaveSizeDefinition, useSizeDefinitions } from '../../api/queries'
import type { SizeDefinition } from '../../api/types'
import { formatSize } from '../../lib/format'

interface RowEdit {
  min?: number
  max?: number
}

/** 0 is a real, meaningful value here: no limit on that end. */
function renderBound(bytes: number): string {
  if (bytes <= 0) return 'Unlimited'
  return formatSize(bytes)
}

function boundError(min: number, max: number): string | null {
  if (min < 0 || max < 0) return 'Sizes cannot be negative.'
  if (min > 0 && max > 0 && min > max) return 'Minimum is above the maximum.'
  return null
}

export function QualityDefinitions() {
  const { data: definitions, isLoading, error } = useSizeDefinitions()
  const save = useSaveSizeDefinition()
  const reset = useResetSizeDefinition()
  const { toast } = useToast()
  const [edits, setEdits] = useState<Record<string, RowEdit>>({})
  const [saving, setSaving] = useState(false)

  const rows = definitions ?? []
  const dirtySlugs = Object.keys(edits)
  const dirty = dirtySlugs.length > 0

  const effective = (row: SizeDefinition): { min: number; max: number } => ({
    min: edits[row.platform_slug]?.min ?? row.min_size,
    max: edits[row.platform_slug]?.max ?? row.max_size,
  })

  const errors = useMemo(() => {
    const out: Record<string, string> = {}
    for (const row of rows) {
      const min = edits[row.platform_slug]?.min ?? row.min_size
      const max = edits[row.platform_slug]?.max ?? row.max_size
      const err = boundError(min, max)
      if (err) out[row.platform_slug] = err
    }
    return out
  }, [rows, edits])

  const setBound = (row: SizeDefinition, end: 'min' | 'max', raw: string) => {
    const parsed = raw.trim() === '' ? 0 : Number(raw)
    if (!Number.isFinite(parsed)) return
    setEdits((prev) => {
      const next = { ...prev, [row.platform_slug]: { ...prev[row.platform_slug], [end]: Math.trunc(parsed) } }
      // Drop the entry entirely once it matches the stored row again, so
      // typing a value and undoing it leaves the page clean.
      const candidate = next[row.platform_slug]
      const min = candidate.min ?? row.min_size
      const max = candidate.max ?? row.max_size
      if (min === row.min_size && max === row.max_size) delete next[row.platform_slug]
      return next
    })
  }

  const onSave = async () => {
    if (Object.keys(errors).length) {
      toast('Fix the highlighted rows before saving.', 'error')
      return
    }
    setSaving(true)
    let ok = 0
    const failed: string[] = []
    // Sequential: a failure stays attributable to one platform, and there are
    // at most a few dirty rows.
    for (const slug of dirtySlugs) {
      const row = rows.find((r) => r.platform_slug === slug)
      if (!row) continue
      const edit = edits[slug]
      try {
        await save.mutateAsync({
          slug,
          min: edit.min !== undefined && edit.min !== row.min_size ? edit.min : undefined,
          max: edit.max !== undefined && edit.max !== row.max_size ? edit.max : undefined,
        })
        ok++
      } catch {
        failed.push(slug)
      }
    }
    setSaving(false)
    setEdits(failed.length ? Object.fromEntries(failed.map((s) => [s, edits[s]])) : {})
    if (failed.length) toast(`Saved ${ok}, failed: ${failed.join(', ')}`, 'error')
    else toast(`Saved ${ok} ${ok === 1 ? 'definition' : 'definitions'}.`, 'success')
  }

  const onReset = async (row: SizeDefinition) => {
    const hadEdits = Boolean(edits[row.platform_slug])
    try {
      await reset.mutateAsync(row.platform_slug)
      setEdits((prev) => {
        const next = { ...prev }
        delete next[row.platform_slug]
        return next
      })
      const what = row.has_catalog ? 'restored from the catalog' : 'cleared — the platform is now unlimited'
      toast(`${row.platform_slug} ${what}${hadEdits ? '; unsaved edits discarded' : ''}.`, 'success')
    } catch (e) {
      toast(e instanceof Error ? e.message : `Could not reset ${row.platform_slug}.`, 'error')
    }
  }

  const boundCell = (row: SizeDefinition, end: 'min' | 'max') => {
    const value = effective(row)[end]
    const rowError = errors[row.platform_slug]
    return (
      <div className="flex flex-col items-end gap-0.5">
        <input
          type="number"
          min={0}
          step={1}
          value={value}
          onChange={(e) => setBound(row, end, e.target.value)}
          className={`${inputCls} w-40 text-right tabular-nums ${rowError ? 'border-red-500/70' : ''}`}
          aria-label={`${end === 'min' ? 'Minimum' : 'Maximum'} size for ${row.platform_slug} in bytes`}
          data-testid={`qd-${end}-${row.platform_slug}`}
        />
        <span className="text-[11px] text-slate-500" data-testid={`qd-${end}-render-${row.platform_slug}`}>
          {renderBound(value)}
        </span>
      </div>
    )
  }

  const columns: Column<SizeDefinition>[] = [
    {
      key: 'platform',
      header: 'Platform',
      sortValue: (r) => r.platform_slug,
      render: (r) => (
        <div>
          <div className="text-slate-200">{r.platform_slug}</div>
          {errors[r.platform_slug] && (
            <div className="mt-0.5 text-xs text-red-400" data-testid={`qd-error-${r.platform_slug}`}>
              {errors[r.platform_slug]}
            </div>
          )}
        </div>
      ),
    },
    { key: 'min', header: 'Minimum', align: 'right', sortValue: (r) => effective(r).min, render: (r) => boundCell(r, 'min') },
    { key: 'max', header: 'Maximum', align: 'right', sortValue: (r) => effective(r).max, render: (r) => boundCell(r, 'max') },
    {
      key: 'source',
      header: 'Source',
      sortValue: (r) => r.source,
      render: (r) => (
        <div data-testid={`qd-source-${r.platform_slug}`}>
          {/* An empty source is a platform with no stored row at all — calling
              that "catalog" would claim a provenance it does not have. */}
          {r.source ? (
            <Badge color={r.source === 'manual' ? 'orange' : 'slate'}>{r.source}</Badge>
          ) : (
            <span className="text-xs text-slate-600">no limits set</span>
          )}
          {/* Only a catalog-derived row can name a snapshot; the store clears
              the version when a row goes manual, so there is nothing to show. */}
          {r.snapshot_version && <div className="mt-0.5 text-[11px] text-slate-500">{r.snapshot_version}</div>}
        </div>
      ),
    },
    {
      key: 'actions',
      header: '',
      align: 'right',
      render: (r) => (
        <Button
          variant="secondary"
          size="sm"
          onClick={() => onReset(r)}
          disabled={reset.isPending}
          data-testid={`qd-reset-${r.platform_slug}`}
        >
          {r.has_catalog ? 'Reset to catalog' : 'Clear limits'}
        </Button>
      ),
    },
  ]

  if (isForbidden(error)) {
    return (
      <PageShell title="Settings" subtitle="Quality Definitions">
        <AdminNotice />
      </PageShell>
    )
  }

  return (
    <PageShell title="Settings" subtitle="Quality Definitions">
      <UnsavedChangesPrompt dirty={dirty} />
      <div data-testid="qd-definitions">
        <FormGroup
          title="Size limits per platform"
          description={
            <>
              A candidate outside its platform&apos;s limits is rejected before it is ever downloaded, and the reason
              names the bound that did it. These are the numbers that do the rejecting — nothing is adjusted behind
              them.{' '}
              <InfoPopover label="Size limits">
                Defaults are derived from the platform&apos;s catalog, with an allowance already folded in for the fact
                that catalog sizes describe uncompressed content while candidates arrive as archives. Edit a value and
                it becomes yours; reset restores the catalog&apos;s. <strong>0 means no limit</strong> on that end.
              </InfoPopover>
            </>
          }
        >
          <div className="pb-3 pt-1">
            <DataTable<SizeDefinition>
              columns={columns}
              rows={rows}
              rowKey={(r) => r.platform_slug}
              loading={isLoading}
              initialSort={{ key: 'platform' }}
              testId="qd-table"
              empty={{
                icon: Ruler,
                title: 'No platforms have size limits',
                hint: 'Platforms get limits when a catalog is imported for them, under Settings → Metadata.',
              }}
            />
            <p className="mt-4 text-xs text-slate-500">
              Platforms without a catalog assignment do not appear here and are unlimited by default.
            </p>
          </div>
        </FormGroup>
      </div>

      <SaveBar
        dirty={dirty}
        saving={saving}
        summary={`${dirtySlugs.length} ${dirtySlugs.length === 1 ? 'platform' : 'platforms'} changed`}
        onSave={onSave}
        onCancel={() => setEdits({})}
      />
    </PageShell>
  )
}
