import { useCallback, useMemo, useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, Gamepad2, Search } from 'lucide-react'
import { useQueries } from '@tanstack/react-query'
import { PageShell } from '../../components/layout/PageShell'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { CoverGrid } from '../../components/ui/CoverGrid'
import { EmptyState } from '../../components/ui/EmptyState'
import { JumpRail } from '../../components/ui/JumpRail'
import { PosterCard } from '../../components/ui/PosterCard'
import { api, qs } from '../../api/client'
import { keys, useLibraryFacets, useLibraryLetters, usePlatforms, type LibraryParams } from '../../api/queries'
import type { LibraryItem, LibraryPage } from '../../api/types'
import { pagesForRange } from '../../lib/coverGridMath'
import { verdictChip } from '../../lib/verdict'

// Level 2: one platform's games (or "all" — the same screen as a group, a
// platform chip per card, never a separate component) as a virtualized cover
// grid. The server owns the ordering, the letter map and the facet counts;
// this screen owns only a sparse page buffer that fills in behind the scroll.

const PAGE_SIZE = 100
// Pages kept mounted as react-query subscriptions. Old pages fall out of the
// window but stay in the query cache, so scrolling back is instant.
const PAGE_WINDOW = 8
// Bottom edge of the frozen chrome: 60px topbar + ~55px sticky toolbar, plus
// breathing room. The rail sticks here and rail jumps land rows just below.
const FROZEN_BAR_CLEARANCE = 124

export function PlatformGames() {
  const params = useParams()
  const slug = params.slug ?? 'all'
  const navigate = useNavigate()

  const [qInput, setQInput] = useState('')
  const [q, setQ] = useState('')
  const [verdict, setVerdict] = useState('')
  const [format, setFormat] = useState('')
  const [pages, setPages] = useState<number[]>([1])

  const platforms = usePlatforms()
  const displayName =
    slug === 'all' ? 'All Games' : (platforms.data?.find((p) => p.id === slug)?.name ?? slug)

  const base: LibraryParams = useMemo(
    () => ({ page: 1, q, platform: slug === 'all' ? 'all' : slug, page_size: PAGE_SIZE, sort: 'title', verdict, format }),
    [q, slug, verdict, format],
  )

  const letters = useLibraryLetters({ q, platform: base.platform, verdict, format })
  const facets = useLibraryFacets(base.platform, q)

  // The sparse buffer: one react-query subscription per window page, so the
  // broad ['library'] invalidation and the cache keep working as everywhere
  // else in the app.
  const pageQueries = useQueries({
    queries: pages.map((p) => ({
      queryKey: keys.library({ ...base, page: p }),
      queryFn: () =>
        api.get<LibraryPage>(
          `/api/library${qs({
            page: p,
            q: base.q,
            platform: base.platform,
            page_size: base.page_size,
            sort: base.sort,
            verdict: base.verdict,
            format: base.format,
          })}`,
        ),
      staleTime: 30_000,
    })),
  })

  const buffer = useMemo(() => {
    const m = new Map<number, LibraryItem>()
    pageQueries.forEach((res, i) => {
      const pageN = pages[i]
      res.data?.items.forEach((item, j) => m.set((pageN - 1) * PAGE_SIZE + j, item))
    })
    return m
  }, [pageQueries, pages])

  const total = letters.data?.total ?? pageQueries[0]?.data?.total ?? 0

  const onRangeChange = useCallback((first: number, last: number) => {
    const needed = pagesForRange(first, last, PAGE_SIZE)
    setPages((cur) => {
      const merged = Array.from(new Set([...cur, ...needed]))
      // Trim to a window around what the viewport needs.
      const lo = Math.min(...needed)
      const hi = Math.max(...needed)
      const windowed = merged.filter((p) => p >= lo - PAGE_WINDOW / 2 && p <= hi + PAGE_WINDOW / 2)
      const next = windowed.length ? windowed.sort((a, b) => a - b) : needed
      return next.length === cur.length && next.every((p, i) => p === cur[i]) ? cur : next
    })
  }, [])

  const scrollToRef = useRef<(offset: number) => void>(() => {})
  const registerScrollToOffset = useCallback((fn: (offset: number) => void) => {
    scrollToRef.current = fn
  }, [])

  const resetAnd = (fn: () => void) => {
    fn()
    setPages([1])
  }

  const facetChips = (dim: 'verdict' | 'format', active: string, set: (v: string) => void) =>
    (facets.data?.facets[dim] ?? []).map((f) => (
      <button
        key={f.value}
        type="button"
        data-testid={`games-facet-${dim}-${f.value}`}
        onClick={() => resetAnd(() => set(active === f.value ? '' : f.value))}
        className={`rounded-full border px-2.5 py-0.5 text-xs ${
          active === f.value
            ? 'border-accent-500 bg-accent-600/20 text-accent-300'
            : 'border-slate-700 bg-slate-800/60 text-slate-400 hover:border-slate-500'
        }`}
      >
        {dim === 'verdict' ? verdictChip(f.value === 'unmeasured' ? '' : f.value).label : f.value} {f.count}
      </button>
    ))

  return (
    <PageShell
      title={displayName}
      subtitle={total ? `${total.toLocaleString()} games` : undefined}
      stickyToolbar
      actions={
        <div className="flex items-center gap-3">
          <Link to="/">
            <Button variant="secondary" size="sm" data-testid="games-back">
              <ArrowLeft className="h-3.5 w-3.5" /> Library
            </Button>
          </Link>
          {/* Context for when the big heading has scrolled away. */}
          <span className="text-sm text-slate-400">
            {displayName}
            {total > 0 && <span className="text-slate-600"> · {total.toLocaleString()}</span>}
          </span>
        </div>
      }
      tools={
        <form
          onSubmit={(e: FormEvent) => {
            e.preventDefault()
            resetAnd(() => setQ(qInput.trim()))
          }}
          className="flex items-center gap-2"
        >
          <input
            value={qInput}
            onChange={(e) => setQInput(e.target.value)}
            placeholder="Title or filename…"
            data-testid="games-filter"
            className="w-56 rounded border border-slate-700 bg-slate-800 px-3 py-1.5 text-sm text-slate-300 placeholder:text-slate-600 focus:border-accent-500 focus:outline-none"
          />
          <Button size="sm" type="submit" variant="secondary">
            <Search className="h-3.5 w-3.5" />
          </Button>
        </form>
      }
    >
      <div className="mb-3 flex flex-wrap items-center gap-1.5" data-testid="games-facets">
        {facetChips('verdict', verdict, setVerdict)}
        <span className="mx-1 h-4 w-px bg-slate-800" />
        {facetChips('format', format, setFormat)}
      </div>

      <div className="flex gap-3">
        <div className="min-w-0 flex-1">
          <CoverGrid
            total={total}
            minCardWidth={150}
            rowHeight={318}
            onRangeChange={onRangeChange}
            registerScrollToOffset={registerScrollToOffset}
            scrollPaddingTop={FROZEN_BAR_CLEARANCE}
            testId="library-grid"
            emptyState={
              <EmptyState
                icon={Gamepad2}
                title={q || verdict || format ? 'Nothing matches these filters' : 'Nothing on this platform yet'}
                hint={q ? 'The search matches titles and filenames.' : undefined}
              />
            }
            renderItem={(index) => {
              const item = buffer.get(index)
              if (!item) {
                return <div className="aspect-[3/4] animate-pulse rounded-xl border border-slate-800 bg-slate-900" />
              }
              const chip = verdictChip(item.catalog_verdict)
              return (
                <PosterCard
                  variant="poster"
                  title={item.title}
                  artSources={[`/api/art/title/${item.id}?size=thumb`]}
                  subtitle={slug === 'all' ? item.platform || item.platform_slug : undefined}
                  chips={
                    <>
                      <Badge color={chip.color}>{chip.label}</Badge>
                      {(item.profile_id ?? 0) > 0 && <Badge color="accent">pinned</Badge>}
                    </>
                  }
                  onClick={() => navigate(`/library/${item.platform_slug ?? slug}/${item.id}`)}
                  testId={`game-card-${item.id}`}
                />
              )
            }}
          />
        </div>
        <JumpRail
          letters={letters.data?.letters ?? []}
          onJump={(offset) => scrollToRef.current(offset)}
          topClass="top-[124px]"
          testId="games-rail"
        />
      </div>
    </PageShell>
  )
}
