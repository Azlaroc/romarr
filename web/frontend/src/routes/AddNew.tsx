import { useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Search, Library, Sparkles } from 'lucide-react'
import {
  useAddWishlist,
  useDatGames,
  useMetadataProviders,
  useMetadataSearch,
  usePlatformRegistry,
  useQualityProfiles,
} from '../api/queries'
import type { DatGame, MetadataGame } from '../api/types'
import { PageShell } from '../components/layout/PageShell'
import { PlatformSelect, usePlatformName } from './PlatformSelect'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { DataTable, type Column } from '../components/ui/DataTable'
import { EmptyState } from '../components/ui/EmptyState'
import { Modal } from '../components/ui/Modal'
import { Skeleton } from '../components/ui/Skeleton'
import { useToast } from '../components/ui/Toast'
import { formatSize } from '../lib/format'

const inputCls =
  'w-full rounded-lg border border-slate-700 bg-slate-800 px-4 py-2.5 text-sm text-white placeholder-slate-500 focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500'

type Door = 'discover' | 'browse'

/**
 * Add New has two doors, because there are two ways to want a game.
 *
 * Discover is art-forward: you half-remember a title, you see the box art,
 * that's the one. Browse is completionist: you pick a platform and read the
 * catalog of every dump that exists for it. The first asks a public metadata
 * authority what games ARE; the second asks the DAT plane what dumps exist.
 * Neither asks the library, which by definition knows nothing about a game
 * you do not own yet.
 */
export function AddNew() {
  const [params] = useSearchParams()
  const [door, setDoor] = useState<Door>('discover')
  const [picked, setPicked] = useState<{ title: string; platforms: string[] } | null>(null)

  return (
    <PageShell
      title="Add New"
      subtitle="Find a game and put it on the wishlist"
      tools={
        <div className="flex gap-1 rounded-lg bg-slate-850 p-1" data-testid="addnew-doors">
          <Button
            size="sm"
            variant={door === 'discover' ? 'primary' : 'secondary'}
            onClick={() => setDoor('discover')}
            data-testid="door-discover"
          >
            <Sparkles className="h-3.5 w-3.5" /> Discover
          </Button>
          <Button
            size="sm"
            variant={door === 'browse' ? 'primary' : 'secondary'}
            onClick={() => setDoor('browse')}
            data-testid="door-browse"
          >
            <Library className="h-3.5 w-3.5" /> Browse a platform
          </Button>
        </div>
      }
    >
      {door === 'discover' ? (
        <DiscoverDoor initialQuery={params.get('q') ?? ''} onPick={setPicked} />
      ) : (
        <BrowseDoor onPick={setPicked} />
      )}

      <AddDialog picked={picked} onClose={() => setPicked(null)} />
    </PageShell>
  )
}

/** Art-forward search against the metadata authority. */
function DiscoverDoor({
  initialQuery,
  onPick,
}: {
  initialQuery: string
  onPick: (v: { title: string; platforms: string[] }) => void
}) {
  const [q, setQ] = useState(initialQuery)
  const [games, setGames] = useState<MetadataGame[] | null>(null)
  const [unavailable, setUnavailable] = useState('')
  const search = useMetadataSearch()
  const providers = useMetadataProviders()
  const { toast } = useToast()

  const configured = (providers.data?.providers ?? []).some((p) => p.configured)

  const run = async (e: FormEvent) => {
    e.preventDefault()
    const query = q.trim()
    if (!query) return
    setUnavailable('')
    try {
      const res = await search.mutateAsync({ q: query })
      setGames(res.games ?? [])
    } catch (err) {
      setGames([])
      // A missing provider is a state to explain, not an error to toast and
      // forget: the operator needs to know WHICH credentials are absent.
      const msg = err instanceof Error ? err.message : 'Search failed'
      setUnavailable(msg)
      toast('Search failed', 'error')
    }
  }

  return (
    <>
      <form onSubmit={run} className="mb-4 flex flex-col gap-3 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500" />
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search for a game…"
            className={`${inputCls} pl-9`}
            data-testid="discover-input"
          />
        </div>
        <Button type="submit" disabled={search.isPending || !q.trim()} data-testid="discover-submit">
          {search.isPending ? 'Searching…' : 'Search'}
        </Button>
      </form>

      {!configured && !providers.isLoading && (
        <div className="mb-4 rounded-lg border border-slate-700 bg-slate-800/60 px-3 py-2 text-xs text-slate-300" data-testid="discover-unconfigured">
          No metadata provider is configured, so this door has nothing to ask. Set the IGDB credentials (Settings →
          Metadata lists them) — or use <strong>Browse a platform</strong>, which reads the DAT catalog and needs no
          credentials.
        </div>
      )}
      {unavailable && (
        <div className="mb-4 rounded-lg border border-red-900/60 bg-red-950/40 px-3 py-2 text-xs text-red-200" data-testid="discover-error">
          {unavailable}
        </div>
      )}

      {search.isPending ? (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
          {Array.from({ length: 10 }).map((_, i) => (
            <Skeleton key={i} className="h-56" />
          ))}
        </div>
      ) : games === null ? (
        <EmptyState icon={Sparkles} title="Search for a game" hint="Cover art makes it obvious which one you meant." />
      ) : games.length === 0 ? (
        <EmptyState icon={Search} title="No games found" />
      ) : (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5" data-testid="discover-results">
          {games.map((g) => (
            <button
              key={g.provider_id}
              onClick={() => onPick({ title: g.name, platforms: g.platforms ?? [] })}
              className="group overflow-hidden rounded-xl border border-slate-800 bg-slate-900 text-left transition-colors hover:border-slate-700"
              data-testid={`discover-game-${g.provider_id}`}
            >
              <div className="flex h-40 items-center justify-center overflow-hidden bg-slate-850">
                {g.cover_url ? (
                  <img src={g.cover_url} alt="" className="h-full w-full object-cover" loading="lazy" />
                ) : (
                  <span className="px-2 text-center text-xs text-slate-600">no cover art</span>
                )}
              </div>
              <div className="p-3">
                <div className="truncate text-sm font-medium text-white" title={g.name}>
                  {g.name}
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-1.5">
                  {g.release_year ? <span className="text-xs text-slate-500">{g.release_year}</span> : null}
                  {(g.platforms ?? []).slice(0, 3).map((p) => (
                    <Badge key={p} color="slate">
                      {p}
                    </Badge>
                  ))}
                </div>
              </div>
            </button>
          ))}
        </div>
      )}
    </>
  )
}

/** The completionist door: the catalog of dumps for one platform. */
function BrowseDoor({ onPick }: { onPick: (v: { title: string; platforms: string[] }) => void }) {
  const [platform, setPlatform] = useState('')
  const [q, setQ] = useState('')
  const [page, setPage] = useState(1)
  const games = useDatGames(platform, q, page, !!platform)

  const columns: Column<DatGame>[] = [
    {
      key: 'name',
      header: 'Dump',
      render: (g) => <span className="text-white">{g.name}</span>,
      sortValue: (g) => g.name,
    },
    { key: 'region', header: 'Region', render: (g) => g.region || '—', sortValue: (g) => g.region ?? '' },
    {
      key: 'total_size',
      header: 'Size',
      align: 'right',
      render: (g) => formatSize(g.total_size) || '—',
      sortValue: (g) => g.total_size,
    },
    {
      key: 'add',
      header: '',
      align: 'right',
      render: (g) => (
        <Button
          size="sm"
          variant="secondary"
          onClick={() => onPick({ title: g.bare_title || g.name, platforms: [platform] })}
          data-testid={`browse-add-${g.id}`}
        >
          Add
        </Button>
      ),
    },
  ]

  return (
    <>
      <div className="mb-4 flex flex-col gap-3 sm:flex-row">
        <PlatformSelect value={platform} onChange={(v) => { setPlatform(v); setPage(1) }} includeAll={false} testid="browse-platform" />
        <input
          value={q}
          onChange={(e) => { setQ(e.target.value); setPage(1) }}
          placeholder="Filter by title…"
          className={`${inputCls} flex-1`}
          data-testid="browse-filter"
        />
      </div>

      {!platform ? (
        <EmptyState icon={Library} title="Pick a platform" hint="Every dump its catalog knows about, listed in full." />
      ) : (
        <>
          <p className="mb-2 text-xs text-slate-500" data-testid="browse-count">
            {games.data ? `${games.data.total} known dumps` : 'Reading the catalog…'}
          </p>
          <DataTable
            columns={columns}
            rows={games.data?.games ?? []}
            loading={games.isLoading}
            rowKey={(g) => String(g.id)}
            empty={{
              icon: Library,
              title: 'No catalogued dumps match',
              hint: 'A platform with no DAT lane has nothing here — Settings → Metadata assigns one.',
            }}
            page={games.data?.page}
            totalPages={games.data ? Math.max(1, Math.ceil(games.data.total / (games.data.page_size || 50))) : undefined}
            onPageChange={setPage}
            testId="browse-table"
          />
        </>
      )}
    </>
  )
}

/**
 * The add dialog: pick the platform, pick the profile, see what the catalog
 * knows before committing. "No known dumps" is said HERE rather than
 * discovered weeks later as a wishlist row that never fills.
 */
function AddDialog({
  picked,
  onClose,
}: {
  picked: { title: string; platforms: string[] } | null
  onClose: () => void
}) {
  const [platform, setPlatform] = useState('')
  const [profileID, setProfileID] = useState(0)
  const { data: profiles } = useQualityProfiles()
  const { data: platformRows } = usePlatformRegistry()
  const platformName = usePlatformName()
  const add = useAddWishlist()
  const { toast } = useToast()

  const chosen = platform || picked?.platforms?.[0] || ''
  const dumps = useDatGames(chosen, picked?.title ?? '', 1, !!picked && !!chosen)
  const selectable = (profiles ?? []).filter((p) => !p.is_template)

  const known = dumps.data?.total
  const autoPick = dumps.data?.games?.[0]?.name

  const defaultProfileName = (): string => {
    const row = (platformRows ?? []).find((p) => p.slug === chosen)
    if (row?.default_profile_id) {
      return selectable.find((p) => p.id === row.default_profile_id)?.name ?? 'the platform default'
    }
    const globalDefault = selectable.find((p) => p.is_default)
    return globalDefault?.name ?? 'built-in defaults'
  }

  const submit = async () => {
    if (!picked || !chosen) return
    try {
      await add.mutateAsync({
        title: picked.title,
        platform: platformName(chosen),
        platform_slug: chosen,
        ...(profileID ? { profile_id: profileID } : {}),
      })
      toast(`Added ${picked.title}`, 'success')
      setPlatform('')
      setProfileID(0)
      onClose()
    } catch {
      toast('Failed to add', 'error')
    }
  }

  return (
    <Modal open={picked !== null} onClose={onClose} title={picked?.title}>
      <div className="space-y-4" data-testid="add-dialog">
        <div>
          <div className="mb-1 text-xs uppercase tracking-wide text-slate-500">Platform</div>
          {picked && picked.platforms.length > 0 ? (
            <div className="flex flex-wrap gap-2" data-testid="add-platforms">
              {picked.platforms.map((p) => (
                <Button
                  key={p}
                  size="sm"
                  variant={chosen === p ? 'primary' : 'secondary'}
                  onClick={() => setPlatform(p)}
                  data-testid={`add-platform-${p}`}
                >
                  {platformName(p)}
                </Button>
              ))}
            </div>
          ) : (
            <PlatformSelect value={chosen} onChange={setPlatform} includeAll={false} testid="add-platform-select" />
          )}
        </div>

        <div>
          <div className="mb-1 text-xs uppercase tracking-wide text-slate-500">Quality profile</div>
          <select
            value={profileID}
            onChange={(e) => setProfileID(Number(e.target.value))}
            className={inputCls}
            aria-label="Quality profile for this title"
            data-testid="add-profile"
          >
            <option value={0}>Platform default ({defaultProfileName()})</option>
            {selectable.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>

        {chosen && (
          <div className="rounded-lg border border-slate-700 bg-slate-800/60 px-3 py-2 text-xs" data-testid="add-dumps">
            {dumps.isLoading ? (
              <span className="text-slate-400">Checking the catalog…</span>
            ) : known === 0 ? (
              <span className="text-orange-300">
                No known dumps for this title on {platformName(chosen)}. It can still be wishlisted, but nothing in the
                catalog matches it — check the platform, or the spelling.
              </span>
            ) : (
              <span className="text-slate-300">
                {known} known {known === 1 ? 'dump' : 'dumps'}
                {autoPick ? <> · the selector will pick from them, starting with <strong>{autoPick}</strong></> : null}
              </span>
            )}
          </div>
        )}

        <div className="flex justify-end gap-2 border-t border-slate-800 pt-4">
          <Button variant="secondary" onClick={onClose} data-testid="add-cancel">
            Cancel
          </Button>
          <Button onClick={submit} disabled={!chosen || add.isPending} data-testid="add-confirm">
            Add to wishlist
          </Button>
        </div>
      </div>
    </Modal>
  )
}
