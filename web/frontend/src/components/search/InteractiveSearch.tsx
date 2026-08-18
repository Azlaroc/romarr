import { useEffect, useState } from 'react'
import { useSearch } from '../../api/queries'
import type { SearchResult } from '../../api/types'
import { Modal } from '../ui/Modal'
import { ReleaseResults } from './ReleaseResults'
import { Button } from '../ui/Button'
import { useToast } from '../ui/Toast'

/**
 * Interactive search — the arrs' manual search, reachable from the row it is
 * about rather than from a separate screen. Give it a wishlist row and the
 * backend resolves that row's own quality profile, so a hand-picked release
 * is ranked under the same policy the automatic grab would have used.
 */
export function InteractiveSearch({
  open,
  onClose,
  title,
  platformSlug,
  wishlistId,
}: {
  open: boolean
  onClose: () => void
  title: string
  platformSlug?: string
  wishlistId?: number
}) {
  const [results, setResults] = useState<SearchResult[] | null>(null)
  const [tookMs, setTookMs] = useState<number | undefined>()
  const search = useSearch()
  const { toast } = useToast()

  useEffect(() => {
    if (!open) {
      setResults(null)
      setTookMs(undefined)
      return
    }
    let cancelled = false
    void (async () => {
      try {
        const res = await search.mutateAsync({
          q: title,
          platform: platformSlug || 'all',
          wishlistId,
        })
        if (cancelled) return
        setResults(res.results ?? [])
        setTookMs(res.search_time_ms)
      } catch {
        if (cancelled) return
        setResults([])
        toast('Search failed', 'error')
      }
    })()
    return () => {
      cancelled = true
    }
    // Re-running on every render of the parent would refire the search; the
    // identity of the row being searched is what should trigger it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, title, platformSlug, wishlistId])

  return (
    <Modal open={open} onClose={onClose} title={`Search: ${title}`} size="lg">
      <div className="space-y-4" data-testid="interactive-search">
        <div className="flex items-center justify-between gap-3">
          <p className="text-xs text-slate-500" data-testid="interactive-search-info">
            {results === null
              ? 'Searching every enabled source…'
              : `${results.length} releases${tookMs != null ? ` in ${tookMs}ms` : ''}${
                  wishlistId ? ' · ranked under this title’s profile' : ''
                }`}
          </p>
          <Button size="sm" variant="secondary" onClick={onClose} data-testid="interactive-search-close">
            Close
          </Button>
        </div>
        <ReleaseResults results={results} pending={search.isPending && results === null} columns={1} />
      </div>
    </Modal>
  )
}
