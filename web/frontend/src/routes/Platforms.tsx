import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Joystick } from 'lucide-react'
import { PageShell } from '../components/layout/PageShell'
import { AdminNotice } from '../components/ui/AdminNotice'
import { Badge } from '../components/ui/Badge'
import { DataTable, type Column } from '../components/ui/DataTable'
import { FormGroup } from '../components/ui/FormGroup'
import { InfoPopover } from '../components/ui/InfoPopover'
import { SaveBar, UnsavedChangesPrompt } from '../components/ui/SaveBar'
import { Toggle } from '../components/ui/Toggle'
import { inputCls } from '../components/ui/Input'
import { useToast } from '../components/ui/Toast'
import { isForbidden } from '../api/client'
import {
  useDatCoverage,
  usePlatformRegistry,
  useQualityProfiles,
  useSavePlatform,
  useSizeDefinitions,
} from '../api/queries'
import type { PlatformRow } from '../api/types'
import { formatSize } from '../lib/format'

interface RowEdit {
  default_profile_id?: number
  acquisition_enabled?: boolean
  collection_mode?: boolean
}

const CLASS_COLOR: Record<string, 'blue' | 'purple' | 'orange' | 'slate'> = {
  carts: 'blue',
  discs: 'purple',
  arcade: 'orange',
}

/** 0 is meaningful on both ends: no limit there. */
function renderBand(min: number, max: number): string {
  if (!min && !max) return 'Unlimited'
  return `${min ? formatSize(min) : '0'} – ${max ? formatSize(max) : '∞'}`
}

export function Platforms() {
  const { data: platforms, isLoading, error } = usePlatformRegistry()
  const { data: profileData } = useQualityProfiles()
  const { data: coverage } = useDatCoverage()
  const { data: sizeDefs } = useSizeDefinitions()
  const save = useSavePlatform()
  const { toast } = useToast()
  const [edits, setEdits] = useState<Record<string, RowEdit>>({})
  const [saving, setSaving] = useState(false)

  // A directory that is not a platform (forwarders, supporting files) is
  // enumerable so the Library filter can reach it, but there is nothing to
  // manage about it here.
  const rows = (platforms ?? []).filter((p) => !p.is_system)
  const dirtySlugs = Object.keys(edits)
  const dirty = dirtySlugs.length > 0

  // Templates are cloned for new platforms, never used directly, so they are
  // not offerable as a platform's default.
  const profiles = (profileData ?? []).filter((p) => !p.is_template)

  const coverageBySlug = useMemo(() => {
    const out: Record<string, string> = {}
    for (const c of coverage?.coverage ?? []) out[c.platform_slug] = c.summary
    return out
  }, [coverage])

  const bandBySlug = useMemo(() => {
    const out: Record<string, string> = {}
    for (const d of sizeDefs ?? []) out[d.platform_slug] = renderBand(d.min_size, d.max_size)
    return out
  }, [sizeDefs])

  const effective = (row: PlatformRow) => ({
    profileID: edits[row.slug]?.default_profile_id ?? row.default_profile_id,
    acquisition: edits[row.slug]?.acquisition_enabled ?? row.acquisition_enabled,
    collection: edits[row.slug]?.collection_mode ?? row.collection_mode,
  })

  const edit = (row: PlatformRow, patch: RowEdit) => {
    setEdits((prev) => {
      const next = { ...prev, [row.slug]: { ...prev[row.slug], ...patch } }
      // Drop the entry once it matches the stored row again, so making a
      // change and undoing it leaves the page clean.
      const cand = next[row.slug]
      const profileID = cand.default_profile_id ?? row.default_profile_id
      const acquisition = cand.acquisition_enabled ?? row.acquisition_enabled
      const collection = cand.collection_mode ?? row.collection_mode
      if (
        profileID === row.default_profile_id &&
        acquisition === row.acquisition_enabled &&
        collection === row.collection_mode
      )
        delete next[row.slug]
      return next
    })
  }

  const onSave = async () => {
    setSaving(true)
    let ok = 0
    const failed: string[] = []
    // Sequential, so a failure stays attributable to one platform.
    for (const slug of dirtySlugs) {
      const row = rows.find((r) => r.slug === slug)
      if (!row) continue
      const e = edits[slug]
      try {
        await save.mutateAsync({
          slug,
          ...(e.default_profile_id !== undefined && e.default_profile_id !== row.default_profile_id
            ? { default_profile_id: e.default_profile_id }
            : {}),
          ...(e.acquisition_enabled !== undefined && e.acquisition_enabled !== row.acquisition_enabled
            ? { acquisition_enabled: e.acquisition_enabled }
            : {}),
          ...(e.collection_mode !== undefined && e.collection_mode !== row.collection_mode
            ? { collection_mode: e.collection_mode }
            : {}),
        })
        ok++
      } catch {
        failed.push(slug)
      }
    }
    setSaving(false)
    setEdits(failed.length ? Object.fromEntries(failed.map((s) => [s, edits[s]])) : {})
    if (failed.length) toast(`Saved ${ok}, failed: ${failed.join(', ')}`, 'error')
    else toast(`Saved ${ok} ${ok === 1 ? 'platform' : 'platforms'}.`, 'success')
  }

  const columns: Column<PlatformRow>[] = [
    {
      key: 'platform',
      header: 'Platform',
      sortValue: (r) => r.display_name.toLowerCase(),
      render: (r) => (
        <div>
          <div className="text-slate-200">{r.display_name}</div>
          <div className="text-[11px] text-slate-500">{r.slug}</div>
        </div>
      ),
    },
    {
      key: 'class',
      header: 'Type',
      sortValue: (r) => r.media_class,
      render: (r) =>
        r.media_class ? (
          <Badge color={CLASS_COLOR[r.media_class] ?? 'slate'}>{r.media_class}</Badge>
        ) : (
          <span className="text-xs text-slate-600">—</span>
        ),
    },
    {
      key: 'catalog',
      header: 'Catalog',
      sortValue: (r) => r.dat_authority ?? '',
      render: (r) => (
        <div data-testid={`plat-catalog-${r.slug}`}>
          {r.dat_authority ? (
            <>
              <Badge color="slate">{r.dat_authority}</Badge>
              {coverageBySlug[r.slug] && (
                <div className="mt-0.5 text-[11px] text-slate-500">{coverageBySlug[r.slug]}</div>
              )}
            </>
          ) : (
            <span className="text-xs text-slate-600">no catalog</span>
          )}
        </div>
      ),
    },
    {
      key: 'profile',
      header: 'Default profile',
      sortValue: (r) => effective(r).profileID,
      render: (r) => (
        <select
          className={`${inputCls} w-52`}
          value={effective(r).profileID}
          onChange={(e) => edit(r, { default_profile_id: Number(e.target.value) })}
          aria-label={`Default quality profile for ${r.display_name}`}
          data-testid={`plat-profile-${r.slug}`}
        >
          {/* 0 is not "none": it means the global default applies, which is
              what a platform nobody has tuned actually uses. */}
          <option value={0}>Global default</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      ),
    },
    {
      key: 'limits',
      header: 'Size limits',
      align: 'right',
      sortValue: (r) => bandBySlug[r.slug] ?? '',
      render: (r) => (
        <Link
          to="/settings/quality-definitions"
          className="text-xs text-slate-400 underline decoration-dotted hover:text-slate-200"
          data-testid={`plat-limits-${r.slug}`}
        >
          {bandBySlug[r.slug] ?? 'Unlimited'}
        </Link>
      ),
    },
    {
      key: 'collection',
      header: 'Collection',
      align: 'right',
      sortValue: (r) => (effective(r).collection ? 0 : 1),
      render: (r) => (
        <div className="flex justify-end">
          <Toggle
            checked={effective(r).collection}
            onChange={(v) => edit(r, { collection_mode: v })}
            label=""
            aria-label={`Collection mode for ${r.display_name}`}
            data-testid={`plat-collection-${r.slug}`}
            disabled={!r.dat_authority}
          />
        </div>
      ),
    },
    {
      key: 'acquisition',
      header: 'Acquisition',
      align: 'right',
      sortValue: (r) => (effective(r).acquisition ? 0 : 1),
      render: (r) => (
        <div className="flex justify-end">
          <Toggle
            checked={effective(r).acquisition}
            onChange={(v) => edit(r, { acquisition_enabled: v })}
            label=""
            aria-label={`Acquisition for ${r.display_name}`}
            data-testid={`plat-acq-${r.slug}`}
          />
        </div>
      ),
    },
  ]

  if (isForbidden(error)) {
    return (
      <PageShell title="Platforms">
        <AdminNotice />
      </PageShell>
    )
  }

  return (
    <PageShell
      title="Platforms"
      subtitle="What RomArr knows about each system, and how it treats it"
    >
      <UnsavedChangesPrompt dirty={dirty} />
      <div data-testid="plat-platforms">
        <FormGroup
          title="Systems"
          description={
            <>
              Every platform RomArr knows about, not only the ones already in your library — adding a game for a
              system you have never acquired for needs no setup first.{' '}
              <InfoPopover label="Platforms">
                A platform&apos;s <strong>default profile</strong> applies to titles added for it that do not choose
                their own. The first title added on a platform with no default gets one created from its type&apos;s
                template. Turning <strong>acquisition</strong> off stops searching and grabbing for that platform;
                anything already on the wishlist stays there and resumes when you turn it back on.{' '}
                <strong>Collection</strong> monitors the platform&apos;s whole 1G1R set — one dump per game, chosen by
                the platform&apos;s profile — and everything missing from it becomes wanted work, listed under Wanted →
                Collection. It needs a catalog, so platforms with no DAT authority cannot use it. The two switches are
                independent: with collection on and acquisition off you can watch the gap list without RomArr acting
                on it.
              </InfoPopover>
            </>
          }
        >
          <div className="pb-3 pt-1">
            <DataTable<PlatformRow>
              columns={columns}
              rows={rows}
              rowKey={(r) => r.slug}
              loading={isLoading}
              initialSort={{ key: 'platform' }}
              testId="plat-table"
              empty={{
                icon: Joystick,
                title: 'No platforms',
                hint: 'The shipped platform vocabulary seeds itself on first start.',
              }}
            />
            <p className="mt-4 text-xs text-slate-500">
              Identity — the IGDB slug, the on-disk directory name, indexer categories — is the vocabulary itself and
              is not editable here. Catalog assignments live under Settings → Metadata; size limits under Settings →
              Quality Definitions.
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
