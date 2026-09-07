import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, FileSearch, ShieldCheck, Trash2 } from 'lucide-react'
import { PageShell } from '../../components/layout/PageShell'
import { ArtImage } from '../../components/ui/ArtImage'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { DataTable, type Column } from '../../components/ui/DataTable'
import { Select } from '../../components/ui/Select'
import { Skeleton } from '../../components/ui/Skeleton'
import { InteractiveSearch } from '../../components/search/InteractiveSearch'
import { useToast } from '../../components/ui/Toast'
import {
  useClearDumpOverride,
  useDatGameRoms,
  useDeleteLibraryItem,
  useItemActivity,
  useLibraryDetail,
  useQualityProfiles,
  useSetDumpOverride,
  useSetLibraryItemProfile,
  useVerifyLibraryItem,
} from '../../api/queries'
import { api } from '../../api/client'
import type { DatRom } from '../../api/types'
import type { LibraryDetail } from '../../api/types'
import { formatSize } from '../../lib/format'
import { verdictChip } from '../../lib/verdict'
import { parseSelectorDecision } from '../../lib/selectorDecision'

// Level 3: one game, everything known about it — the Radarr movie-detail
// shape. Every panel is DB-derived; the one button that touches the file
// says so ("Verify now" pays the measurement the zero-I/O scan skipped).

const OUTCOME_COPY: Record<string, string> = {
  resolved: '',
  nomatch: 'hashed — the catalog has never heard of these bytes',
  ambiguous: 'byte-identical dumps under different catalog names — a human call',
  unhashed: 'never measured — verify to find out',
}

export function GameDetail() {
  const params = useParams()
  const id = Number(params.id ?? 0)
  const navigate = useNavigate()
  const { toast } = useToast()

  const { data, isLoading } = useLibraryDetail(id)
  const profiles = useQualityProfiles()
  const setProfile = useSetLibraryItemProfile()
  const verify = useVerifyLibraryItem()
  const del = useDeleteLibraryItem()
  const setOverride = useSetDumpOverride()
  const clearOverride = useClearDumpOverride()

  // "That!": pin one catalogued dump. The pin stores the dump's NAME plus
  // its roms' hashes (fetched at click time — a catalog row id would be
  // snapshot-scoped and die on the next refresh).
  const pinDump = async (gameId: number, name: string) => {
    try {
      const res = await api.get<{ roms: DatRom[] }>(`/api/dat/games/${gameId}/roms`)
      const hashes = (res.roms ?? [])
        .flatMap((r) => [r.md5, r.sha1])
        .filter((h): h is string => !!h)
      await setOverride.mutateAsync({ id: item.id, dumpName: name, hashes })
      toast(`Pinned: the next grab must be ${name}`, 'success')
    } catch {
      toast('Failed to pin the dump', 'error')
    }
  }

  const [searching, setSearching] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [expandedDump, setExpandedDump] = useState(0)
  const [verifyPolling, setVerifyPolling] = useState(false)

  // Verify Now is a 202 — poll the detail read until the verdict lands.
  const detailRef = useRef(data)
  detailRef.current = data
  useEffect(() => {
    if (!verifyPolling) return
    const t = setInterval(() => {
      if (detailRef.current?.item.catalog_verdict) {
        setVerifyPolling(false)
      }
    }, 2000)
    return () => clearInterval(t)
  }, [verifyPolling])

  if (isLoading || !data) {
    return (
      <PageShell title="Library">
        <Skeleton className="h-64" />
      </PageShell>
    )
  }

  const { item, hashes, canonical, dat_group, set, profile, igdb, override } = data
  const slug = item.platform_slug ?? ''
  const chip = verdictChip(verifyPolling && !item.catalog_verdict ? undefined : item.catalog_verdict)

  return (
    <PageShell
      title="Library"
      subtitle={item.title}
      actions={
        <Link to={`/library/${slug}`}>
          <Button variant="secondary" size="sm" data-testid="detail-back">
            <ArrowLeft className="h-3.5 w-3.5" /> {item.platform || slug}
          </Button>
        </Link>
      }
      tools={
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => setSearching(true)} data-testid="detail-search">
            <FileSearch className="h-3.5 w-3.5" /> Interactive search
          </Button>
          <Button variant="danger" size="sm" onClick={() => setConfirmDelete(true)} data-testid="detail-delete">
            <Trash2 className="h-3.5 w-3.5" /> Remove
          </Button>
        </div>
      }
    >
      <div className="space-y-4">
        {/* ── Header: cover + identity + profile ─────────────────────── */}
        <Card>
          <div className="flex gap-5">
            <div className="w-36 shrink-0 overflow-hidden rounded-lg bg-slate-850" data-testid="detail-cover">
              <ArtImage
                sources={[`/api/art/title/${item.id}`]}
                placeholder={
                  <div className="flex aspect-[3/4] items-center justify-center text-3xl font-bold text-slate-700">
                    {(item.title || '?').slice(0, 1).toUpperCase()}
                  </div>
                }
                className="aspect-[3/4] w-full object-cover"
              />
            </div>
            <div className="min-w-0 flex-1 space-y-2">
              <h2 className="truncate text-xl font-semibold text-slate-100" data-testid="detail-title">
                {item.title}
              </h2>
              <div className="flex flex-wrap items-center gap-2 text-sm text-slate-400">
                <Link to={`/library/${slug}`} className="text-accent-400 hover:underline">
                  {item.platform || slug}
                </Link>
                {igdb?.year ? <span>· {igdb.year}</span> : null}
                {igdb?.genres?.length ? <span>· {igdb.genres.join(', ')}</span> : null}
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Badge color={chip.color}>{chip.label}</Badge>
                {set && (
                  <Badge color={set.degraded ? 'orange' : 'blue'}>
                    disc set {set.have.length}/{set.total}
                    {set.degraded ? ' · degraded' : ''}
                  </Badge>
                )}
              </div>
              <div className="flex items-center gap-2 pt-1">
                <span className="text-xs text-slate-500">Profile</span>
                <Select
                  value={String(profile.id)}
                  onChange={async (v: string) => {
                    try {
                      await setProfile.mutateAsync({ id: item.id, profileId: Number(v) })
                      toast(Number(v) === 0 ? 'Profile follows the platform default' : 'Profile pinned for this title', 'success')
                    } catch {
                      toast('Failed to change profile', 'error')
                    }
                  }}
                  options={[
                    { value: '0', label: `Platform default (${profile.resolved_from === 'platform' ? profile.resolved_name : '…'})` },
                    ...(profiles.data ?? [])
                      .filter((p) => !p.is_template)
                      .map((p) => ({ value: String(p.id), label: p.name })),
                  ]}
                  data-testid="detail-profile"
                />
                {profile.resolved_from === 'title' && <Badge color="accent">pinned</Badge>}
              </div>
            </div>
          </div>
        </Card>

        {/* ── File panel ─────────────────────────────────────────────── */}
        <Card title="File">
          <dl className="grid grid-cols-1 gap-x-8 gap-y-2 text-sm sm:grid-cols-2" data-testid="detail-file">
            <Field label="Path" mono value={item.file_path || '—'} />
            <Field label="Size" value={formatSize(item.file_size)} />
            <Field label="File name" mono value={item.fs_name || '—'} />
            <Field
              label="Canonical name"
              mono
              value={canonical.name || (canonical.stems?.length ? canonical.stems.join(' | ') : '—')}
              hint={OUTCOME_COPY[canonical.outcome]}
            />
            <Field
              label={`Hashes${hashes.domain ? ` (${hashes.domain})` : ''}`}
              mono
              value={
                hashes.crc || hashes.md5 || hashes.sha1
                  ? [hashes.crc && `crc ${hashes.crc}`, hashes.md5 && `md5 ${hashes.md5}`, hashes.sha1 && `sha1 ${hashes.sha1}`]
                      .filter(Boolean)
                      .join(' · ')
                  : 'none stored'
              }
            />
            {hashes.unh && (
              <Field
                label={`Payload (${hashes.unh.header || 'headerless'})`}
                mono
                value={[hashes.unh.crc && `crc ${hashes.unh.crc}`, hashes.unh.sha1 && `sha1 ${hashes.unh.sha1}`]
                  .filter(Boolean)
                  .join(' · ')}
              />
            )}
          </dl>
          <div className="mt-3 flex items-center gap-3 border-t border-slate-800 pt-3">
            {hashes.hash_skipped ? (
              <p className="text-xs text-slate-500" data-testid="detail-verify-blocked">
                Cannot be hashed: {hashes.hash_skipped} — a multi-file entry has no single ROM identity.
              </p>
            ) : (
              <>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={verify.isPending || verifyPolling}
                  data-testid="detail-verify"
                  onClick={async () => {
                    try {
                      await verify.mutateAsync(item.id)
                      setVerifyPolling(true)
                      toast('Measuring — the verdict lands here when done', 'success')
                    } catch {
                      toast('Verify failed to start', 'error')
                    }
                  }}
                >
                  <ShieldCheck className="h-3.5 w-3.5" /> {verifyPolling ? 'Measuring…' : 'Verify now'}
                </Button>
                {!item.catalog_verdict && !verifyPolling && (
                  <p className="text-xs text-slate-500">
                    Never measured — the routine scan reads nothing; verifying hashes this one file.
                  </p>
                )}
              </>
            )}
          </div>
        </Card>

        {/* ── Known dumps (the catalog's answer for this title). The table
            IS the dump picker: click That! on the one you want and the
            selector hunts exactly that dump — waiting, never substituting. */}
        <Card
          title="Known dumps"
          action={
            override ? (
              <span className="flex items-center gap-2 text-xs text-slate-400" data-testid="detail-override">
                <Badge color="yellow">pinned: {override.dump_name}</Badge>
                <Button
                  size="sm"
                  variant="secondary"
                  data-testid="detail-override-clear"
                  onClick={async () => {
                    try {
                      await clearOverride.mutateAsync(item.id)
                      toast('Pin cleared — policy picks again', 'success')
                    } catch {
                      toast('Failed to clear the pin', 'error')
                    }
                  }}
                >
                  Unpin
                </Button>
              </span>
            ) : undefined
          }
        >
          <DataTable<LibraryDetail['dat_group'][number]>
            columns={dumpColumns(expandedDump, setExpandedDump, pinDump)}
            rows={dat_group}
            rowKey={(g) => String(g.id)}
            empty={{ icon: FileSearch, title: 'No catalogued dumps for this title on this platform' }}
            testId="detail-dumps"
          />
          {expandedDump > 0 && <DumpFiles gameId={expandedDump} />}
        </Card>

        {/* ── History ────────────────────────────────────────────────── */}
        <ItemHistory id={item.id} />
      </div>

      <InteractiveSearch
        open={searching}
        onClose={() => setSearching(false)}
        title={item.title}
        platformSlug={slug}
        libraryItemId={item.id}
      />
      <ConfirmDialog
        open={confirmDelete}
        title="Remove from library"
        message={`Remove "${item.title}" from the library? The file on disk stays where it is — this deletes RomArr's record of it.`}
        confirmLabel="Remove"
        danger
        onCancel={() => setConfirmDelete(false)}
        onConfirm={async () => {
          try {
            await del.mutateAsync(item.id)
            toast(`Removed ${item.title}`, 'success')
            navigate(`/library/${slug}`)
          } catch {
            toast('Failed to remove', 'error')
          }
        }}
      />
    </PageShell>
  )
}

function Field({ label, value, mono, hint }: { label: string; value: string; mono?: boolean; hint?: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs uppercase tracking-wide text-slate-500">{label}</dt>
      <dd className={`truncate text-slate-300 ${mono ? 'font-mono text-xs' : ''}`} title={value}>
        {value}
      </dd>
      {hint ? <p className="text-xs text-slate-600">{hint}</p> : null}
    </div>
  )
}

function dumpColumns(
  expanded: number,
  setExpanded: (id: number) => void,
  pinDump: (gameId: number, name: string) => void,
): Column<LibraryDetail['dat_group'][number]>[] {
  return [
    {
      key: 'name',
      header: 'Dump',
      render: (g) => (
        <span className="font-mono text-xs text-slate-300">
          {g.name}
          {g.is_current && (
            <Badge color="emerald" className="ml-2">
              yours
            </Badge>
          )}
        </span>
      ),
      sortValue: (g) => g.name,
    },
    { key: 'region', header: 'Region', render: (g) => g.region || '—', sortValue: (g) => g.region ?? '' },
    { key: 'rev', header: 'Rev', render: (g) => (g.revision ? `r${g.revision}` : '—'), align: 'right' },
    { key: 'size', header: 'Size', render: (g) => formatSize(g.total_size), align: 'right', sortValue: (g) => g.total_size ?? 0 },
    {
      key: 'files',
      header: '',
      render: (g) => (
        <span className="flex items-center justify-end gap-1.5">
          {g.is_override ? (
            <Badge color="yellow">pinned</Badge>
          ) : (
            <Button size="sm" variant="secondary" onClick={() => pinDump(g.id, g.name)} data-testid={`detail-dump-that-${g.id}`}>
              That!
            </Button>
          )}
          <Button size="sm" variant="secondary" onClick={() => setExpanded(expanded === g.id ? 0 : g.id)} data-testid={`detail-dump-files-${g.id}`}>
            {expanded === g.id ? 'Hide files' : 'Files'}
          </Button>
        </span>
      ),
      align: 'right',
    },
  ]
}

function DumpFiles({ gameId }: { gameId: number }) {
  const { data, isLoading } = useDatGameRoms(gameId)
  if (isLoading) return <Skeleton className="mt-2 h-10" />
  return (
    <div className="mt-2 rounded-lg border border-slate-800 bg-slate-950/50 p-3" data-testid="detail-dump-roms">
      {(data?.roms ?? []).map((r) => (
        <div key={r.name} className="flex items-center justify-between gap-4 py-0.5 font-mono text-xs text-slate-400">
          <span className="truncate">{r.name}</span>
          <span className="shrink-0">
            {r.crc ? `crc ${r.crc}` : r.sha1 ? `sha1 ${(r.sha1 ?? '').slice(0, 12)}…` : ''} · {formatSize(r.size)}
          </span>
        </div>
      ))}
    </div>
  )
}

function ItemHistory({ id }: { id: number }) {
  const [page, setPage] = useState(1)
  const { data } = useItemActivity(id, page)
  const entries = data?.entries ?? []
  return (
    <Card title="History">
      <div data-testid="detail-history">
        {entries.length === 0 && <p className="py-4 text-center text-sm text-slate-500">Nothing has happened to this title yet.</p>}
        {entries.map((e) => {
          const dec = parseSelectorDecision(e.detail ?? '')
          return (
            <div key={e.id} className="flex items-start justify-between gap-4 border-b border-slate-800/60 py-2 last:border-0">
              <div className="min-w-0">
                <Badge color={e.event_type === 'verify' ? 'blue' : 'slate'}>{e.event_type}</Badge>
                <span className="ml-2 text-sm text-slate-300">{dec ? `${dec.action} (${dec.mode})` : e.detail}</span>
                {dec && <p className="truncate text-xs text-slate-500">{dec.rest}</p>}
              </div>
              {/* Raw server timestamp, per house convention. */}
              <span className="shrink-0 text-xs text-slate-500">{(e.timestamp ?? '').replace('T', ' ').slice(0, 16)}</span>
            </div>
          )
        })}
        {(data?.total ?? 0) > 50 && (
          <div className="flex justify-center gap-2 pt-2">
            <Button size="sm" variant="secondary" disabled={page <= 1} onClick={() => setPage(page - 1)}>
              Newer
            </Button>
            <Button size="sm" variant="secondary" disabled={page * 50 >= (data?.total ?? 0)} onClick={() => setPage(page + 1)}>
              Older
            </Button>
          </div>
        )}
      </div>
    </Card>
  )
}
