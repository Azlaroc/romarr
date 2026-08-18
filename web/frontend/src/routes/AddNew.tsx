import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Search } from 'lucide-react'
import { useSearch } from '../api/queries'
import type { SearchResult } from '../api/types'
import { PageHeader } from '../components/layout/PageHeader'
import { PlatformSelect } from './PlatformSelect'
import { Button } from '../components/ui/Button'
import { ReleaseResults } from '../components/search/ReleaseResults'
import { useToast } from '../components/ui/Toast'

const inputCls =
  'w-full rounded-lg border border-slate-700 bg-slate-800 px-4 py-2.5 text-sm text-white placeholder-slate-500 focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500'

export function AddNew() {
  const [params] = useSearchParams()
  const initialQ = params.get('q') ?? ''
  const [qInput, setQInput] = useState(initialQ)
  const [platform, setPlatform] = useState('all')
  const [results, setResults] = useState<SearchResult[] | null>(null)
  const [tookMs, setTookMs] = useState<number | undefined>()

  const search = useSearch()
  const { toast } = useToast()
  const didAuto = useRef(false)

  const run = async (q: string) => {
    const query = q.trim()
    if (!query) return
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

      <ReleaseResults results={results} pending={search.isPending} />
    </>
  )
}
