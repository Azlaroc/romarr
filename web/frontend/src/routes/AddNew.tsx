import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Search, Download, Check, HardDriveDownload } from 'lucide-react'
import { useSearch, useDownloadGame } from '../api/queries'
import type { SearchResult } from '../api/types'
import { PageHeader } from '../components/layout/PageHeader'
import { PlatformSelect } from './PlatformSelect'
import { Badge, type BadgeColor } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { EmptyState } from '../components/ui/EmptyState'
import { Skeleton } from '../components/ui/Skeleton'
import { useToast } from '../components/ui/Toast'
import { platformBadgeColor } from '../lib/format'

const inputCls =
  'w-full rounded-lg border border-slate-700 bg-slate-800 px-4 py-2.5 text-sm text-white placeholder-slate-500 focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500'

function safety(score = 50): { color: BadgeColor; label: string } {
  if (score >= 70) return { color: 'emerald', label: 'Safe' }
  if (score >= 40) return { color: 'yellow', label: 'Caution' }
  return { color: 'red', label: 'Risky' }
}
function sourceBadge(r: SearchResult): { color: BadgeColor; label: string } {
  if (r.source_type === 'ddl') return { color: 'purple', label: 'DDL' }
  if (r.download_protocol === 'nzb') return { color: 'orange', label: 'NZB' }
  return { color: 'blue', label: 'Torrent' }
}

export function AddNew() {
  const [params] = useSearchParams()
  const initialQ = params.get('q') ?? ''
  const [qInput, setQInput] = useState(initialQ)
  const [platform, setPlatform] = useState('all')
  const [results, setResults] = useState<SearchResult[] | null>(null)
  const [tookMs, setTookMs] = useState<number | undefined>()
  const [sent, setSent] = useState<Set<number>>(new Set())

  const search = useSearch()
  const download = useDownloadGame()
  const { toast } = useToast()
  const didAuto = useRef(false)

  const run = async (q: string) => {
    const query = q.trim()
    if (!query) return
    setSent(new Set())
    try {
      const res = await search.mutateAsync({ q: query, platform })
      setResults(res.results ?? [])
      setTookMs(res.search_time_ms)
    } catch {
      setResults([])
      toast('Search failed', 'error')
    }
  }

  // Auto-run once if navigated in with ?q= (e.g. from the wishlist).
  useEffect(() => {
    if (!didAuto.current && initialQ) {
      didAuto.current = true
      void run(initialQ)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialQ])

  const grab = async (r: SearchResult, idx: number) => {
    try {
      const res = await download.mutateAsync(r)
      if (res.success) {
        setSent((prev) => new Set(prev).add(idx))
        toast(`Downloading: ${r.title}`, 'success')
      } else {
        toast(res.error || 'Failed to queue', 'error')
      }
    } catch {
      toast('Failed to queue', 'error')
    }
  }

  return (
    <>
      <PageHeader title="Add New" subtitle="Search indexers and direct sources" />

      <form onSubmit={(e: FormEvent) => { e.preventDefault(); void run(qInput) }} className="mb-4 flex flex-col gap-3 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500" />
          <input autoFocus value={qInput} onChange={(e) => setQInput(e.target.value)} placeholder="Search for games…" className={`${inputCls} pl-9`} data-testid="search-input" />
        </div>
        <PlatformSelect value={platform} onChange={setPlatform} testid="search-platform" />
        <Button type="submit" disabled={search.isPending || !qInput.trim()} data-testid="search-submit">
          {search.isPending ? 'Searching…' : 'Search'}
        </Button>
      </form>

      {results !== null && (
        <p className="mb-4 text-sm text-slate-500" data-testid="search-info">
          {results.length} results{tookMs != null ? ` in ${tookMs}ms` : ''}
        </p>
      )}

      {search.isPending ? (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-32" />
          ))}
        </div>
      ) : results === null ? (
        <EmptyState icon={HardDriveDownload} title="Search for a game to get started" />
      ) : results.length === 0 ? (
        <EmptyState icon={Search} title="No results found" />
      ) : (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3" data-testid="results">
          {results.map((r, i) => {
            const s = safety(r.safety_score)
            const src = sourceBadge(r)
            const isDDL = r.source_type === 'ddl'
            const warnings = (r.safety_warnings ?? []).join(' · ')
            return (
              <div key={i} className={`rounded-xl border border-slate-800 bg-slate-900 p-4 ${(r.safety_score ?? 50) < 40 ? 'opacity-70' : ''}`}>
                <div className="flex items-start gap-3">
                  <div className="flex min-w-[58px] flex-col items-center gap-1.5 pt-0.5">
                    <Badge color={platformBadgeColor(r.is_pc)}>{r.platform || '?'}</Badge>
                    <Badge color={src.color}>{src.label}</Badge>
                    <Badge color={s.color}>{s.label}</Badge>
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="break-words text-sm font-medium leading-snug text-white">{r.title}</div>
                    <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1 text-xs text-slate-400">
                      {r.indexer && <span>{r.indexer}</span>}
                      {isDDL ? (
                        <span className="text-purple-400">Direct</span>
                      ) : (
                        <>
                          <span className="text-emerald-400">{r.seeders ?? 0} seeds</span>
                          <span>{r.leechers ?? 0} leech</span>
                        </>
                      )}
                      {r.size_human && <span>{r.size_human}</span>}
                    </div>
                    {warnings && <div className="mt-1 text-xs text-red-400/70">{warnings}</div>}
                  </div>
                  <Button
                    size="sm"
                    variant={sent.has(i) ? 'secondary' : 'primary'}
                    disabled={sent.has(i) || download.isPending}
                    onClick={() => grab(r, i)}
                    data-testid={`dl-btn-${i}`}
                  >
                    {sent.has(i) ? <Check className="h-3.5 w-3.5" /> : <Download className="h-3.5 w-3.5" />}
                    {sent.has(i) ? 'Sent' : 'Get'}
                  </Button>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </>
  )
}
