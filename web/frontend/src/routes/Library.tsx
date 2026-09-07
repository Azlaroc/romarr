import { Link, useNavigate } from 'react-router-dom'
import { Archive, ExternalLink, Fingerprint, FolderSearch, Gamepad2, Wand2 } from 'lucide-react'
import { useConfig, useLibraryPlatforms } from '../api/queries'
import type { PlatformTile } from '../api/types'
import { PageShell } from '../components/layout/PageShell'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { EmptyState } from '../components/ui/EmptyState'
import { PosterCard } from '../components/ui/PosterCard'
import { Skeleton } from '../components/ui/Skeleton'
import { formatSize } from '../lib/format'

// Browse level 1: the shelf. The library lands on platforms as DESTINATIONS —
// hardware on its accent gradient, what you own, what the set still wants —
// not an A-Z flood with a filter rail. Policy lives on /platforms; this page
// is inventory. Titles are two clicks away and search lives on level 2,
// where the server matches titles and filenames.

const TILE_GRID = 'grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4'

export function Library() {
  const navigate = useNavigate()
  const { data, isLoading } = useLibraryPlatforms()
  const { data: config } = useConfig()

  const tiles = data?.platforms ?? []
  const totals = data?.totals

  return (
    <PageShell
      title="Library"
      subtitle={
        totals ? `${totals.owned.toLocaleString()} games · ${formatSize(totals.size_bytes)}` : undefined
      }
      actions={
        <div className="flex flex-wrap items-center gap-2">
          <ToolLink to="/library/rename" icon={Wand2} label="Rename" testId="library-rename-link" />
          <ToolLink to="/library/declutter" icon={Archive} label="Declutter" testId="library-declutter-link" />
          <ToolLink to="/library/hashes" icon={Fingerprint} label="Hashes" testId="library-hash-link" />
          <ToolLink to="/library/scan" icon={FolderSearch} label="Scan" testId="library-scan-link" />
          {config?.romm_url && (
            <a href={config.romm_url} target="_blank" rel="noreferrer">
              <Button variant="secondary" size="sm">
                <ExternalLink className="h-3.5 w-3.5" /> RomM
              </Button>
            </a>
          )}
        </div>
      }
    >
      {/* Mounted even while empty/loading so tests can watch tiles appear. */}
      <div className={TILE_GRID} data-testid="platform-grid">
        {isLoading && tiles.length === 0 && Array.from({ length: 8 }).map((_, i) => <Skeleton key={i} className="aspect-[16/10]" />)}
        {!isLoading && tiles.length === 0 && (
          <div className="col-span-full">
            <EmptyState icon={Gamepad2} title="Nothing in the library yet" hint="Add a game, or point the scanner at your ROM folders." />
          </div>
        )}
        {tiles.length > 0 && (
          <PosterCard
            variant="photo"
            title="All Games"
            accent="#7c5cff"
            artSources={[]}
            subtitle={totals ? `${totals.owned.toLocaleString()} games · ${formatSize(totals.size_bytes)}` : undefined}
            onClick={() => navigate('/library/all')}
            testId="platform-card-all"
          />
        )}
        {tiles.map((t) => (
          <PosterCard
            key={t.slug}
            variant="photo"
            title={t.display_name}
            accent={t.accent_color}
            artSources={t.art_url ? [t.art_url] : []}
            subtitle={`${t.owned.toLocaleString()} games · ${formatSize(t.size_bytes)}`}
            badge={t.collection_mode ? <Badge color="accent">collecting</Badge> : undefined}
            chips={<TileChips tile={t} />}
            onClick={() => navigate(`/library/${t.slug}`)}
            testId={`platform-card-${t.slug}`}
          />
        ))}
      </div>
    </PageShell>
  )
}

// The quadrant chips reuse the set view's vocabulary and colors (owned
// emerald / covered blue / gaps orange). They render only when a stamped
// rollup exists — honest absence beats invented zeros — and the stamp's age
// rides the hover, with the live set page one click away via /platforms.
function TileChips({ tile }: { tile: PlatformTile }) {
  const sc = tile.set_counts
  if (!sc) return null
  const stamp = `as of ${sc.computed_at.replace('T', ' ').slice(0, 16)} — the set page is the live answer`
  return (
    <span title={stamp} className="flex flex-wrap gap-1">
      <Badge color="emerald">{sc.owned.toLocaleString()} owned</Badge>
      {sc.covered > 0 && <Badge color="blue">{sc.covered.toLocaleString()} covered</Badge>}
      {sc.gaps > 0 && <Badge color="orange">{sc.gaps.toLocaleString()} gaps</Badge>}
    </span>
  )
}

function ToolLink({ to, icon: Icon, label, testId }: { to: string; icon: typeof Wand2; label: string; testId: string }) {
  return (
    <Link to={to}>
      <Button variant="secondary" size="sm" data-testid={testId}>
        <Icon className="h-3.5 w-3.5" /> {label}
      </Button>
    </Link>
  )
}
