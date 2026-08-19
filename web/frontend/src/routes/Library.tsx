import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { Gamepad2, Search, ExternalLink, Trash2, Wand2, Archive } from 'lucide-react'
import { useConfig, useLibrary, useDeleteLibraryItem } from '../api/queries'
import type { LibraryItem } from '../api/types'
import { PageHeader } from '../components/layout/PageHeader'
import { PlatformSelect } from './PlatformSelect'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { Modal } from '../components/ui/Modal'
import { Pagination } from '../components/ui/Pagination'
import { EmptyState } from '../components/ui/EmptyState'
import { Skeleton } from '../components/ui/Skeleton'
import { useToast } from '../components/ui/Toast'
import { InteractiveSearch } from '../components/search/InteractiveSearch'
import { formatSize, platformBadgeColor } from '../lib/format'

const GRADIENTS = [
  'from-accent-600 to-purple-600',
  'from-emerald-600 to-teal-600',
  'from-orange-600 to-red-600',
  'from-pink-600 to-rose-600',
  'from-cyan-600 to-blue-600',
  'from-violet-600 to-fuchsia-600',
]

const GRID_CLASS = 'grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5'
const inputCls =
  'w-full rounded-lg border border-slate-700 bg-slate-800 px-4 py-2 text-sm text-white placeholder-slate-500 focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500'

export function Library() {
  const [page, setPage] = useState(1)
  const [qInput, setQInput] = useState('')
  const [qActive, setQActive] = useState('')
  const [platform, setPlatform] = useState('all')
  const [selected, setSelected] = useState<LibraryItem | null>(null)
  // Searching from a library item is how you replace a bad dump or take a
  // better one: same pipeline, seeded with what is already owned.
  const [searching, setSearching] = useState<LibraryItem | null>(null)

  const { data, isLoading, isError } = useLibrary({ page, q: qActive, platform })
  const { data: config } = useConfig()
  const del = useDeleteLibraryItem()
  const { toast } = useToast()

  const submitSearch = (e: FormEvent) => {
    e.preventDefault()
    setQActive(qInput.trim())
    setPage(1)
  }

  const remove = async (item: LibraryItem) => {
    try {
      await del.mutateAsync(item.id)
      toast(`Removed ${item.title}`, 'success')
      setSelected(null)
    } catch {
      toast('Failed to remove', 'error')
    }
  }

  const items = data?.items ?? []
  const empty = !isLoading && items.length === 0

  return (
    <>
      <PageHeader
        title="Library"
        subtitle={data ? `${data.total} games` : undefined}
        actions={
          <>
            <Link
              to={`/library/rename?platform=${platform}`}
              className="inline-flex items-center gap-1.5 rounded-lg border border-purple-600/30 bg-purple-600/15 px-3 py-2 text-sm font-medium text-purple-400 hover:bg-purple-600/25"
              data-testid="library-rename-link"
            >
              Rename <Wand2 className="h-3.5 w-3.5" />
            </Link>
            <Link
              to="/library/declutter"
              className="inline-flex items-center gap-1.5 rounded-lg border border-purple-600/30 bg-purple-600/15 px-3 py-2 text-sm font-medium text-purple-400 hover:bg-purple-600/25"
              data-testid="library-declutter-link"
            >
              Declutter <Archive className="h-3.5 w-3.5" />
            </Link>
            {config?.romm_url && (
              <a href={config.romm_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 rounded-lg border border-emerald-600/30 bg-emerald-600/15 px-3 py-2 text-sm font-medium text-emerald-400 hover:bg-emerald-600/25">
                RomM <ExternalLink className="h-3.5 w-3.5" />
              </a>
            )}
          </>
        }
      />

      <form onSubmit={submitSearch} className="mb-6 flex flex-col gap-3 sm:flex-row">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500" />
          <input
            value={qInput}
            onChange={(e) => setQInput(e.target.value)}
            placeholder="Filter library…"
            className={`${inputCls} pl-9`}
            data-testid="library-filter"
          />
        </div>
        <PlatformSelect value={platform} onChange={(v) => { setPlatform(v); setPage(1) }} testid="library-platform" />
        <Button type="submit" variant="secondary">Filter</Button>
      </form>

      {isError ? (
        <EmptyState icon={Gamepad2} title="Couldn’t load the library" />
      ) : (
        <>
          {/* library-grid stays mounted (empty or full) so tests + polling can
              observe items appearing without the container disappearing. */}
          <div className={GRID_CLASS} data-testid="library-grid">
            {isLoading
              ? Array.from({ length: 10 }).map((_, i) => <Skeleton key={i} className="h-40" />)
              : items.map((item, i) => (
                  <button
                    key={item.id}
                    onClick={() => setSelected(item)}
                    className="group overflow-hidden rounded-xl border border-slate-800 bg-slate-900 text-left transition-colors hover:border-slate-700"
                  >
                    <div className={`flex h-28 items-center justify-center bg-gradient-to-br ${GRADIENTS[i % GRADIENTS.length]}`}>
                      <span className="text-3xl font-black text-white/30">
                        {(item.platform_slug || item.platform || '?').toUpperCase().slice(0, 4)}
                      </span>
                    </div>
                    <div className="p-3">
                      <div className="truncate text-sm font-medium text-white" title={item.title}>
                        {item.title}
                      </div>
                      <div className="mt-1.5 flex items-center gap-2">
                        <Badge color={platformBadgeColor(item.is_pc)}>{item.platform}</Badge>
                        {!!item.file_size && <span className="text-xs text-slate-500">{formatSize(item.file_size)}</span>}
                      </div>
                    </div>
                  </button>
                ))}
          </div>
          {empty && <EmptyState icon={Gamepad2} title="No games in library" hint="Add something from the Add New tab." />}
        </>
      )}

      {data && <Pagination page={data.page} totalPages={data.total_pages} onChange={setPage} />}

      <Modal open={!!selected} onClose={() => setSelected(null)} title={selected?.title}>
        {selected && (
          <div className="space-y-4">
            <dl className="grid grid-cols-2 gap-3 text-sm">
              <Field label="Platform" value={selected.platform} />
              <Field label="Slug" value={selected.platform_slug} />
              <Field label="Size" value={formatSize(selected.file_size) || '—'} />
              <Field label="Source" value={selected.source || selected.source_type || '—'} />
            </dl>
            <div className="flex justify-between border-t border-slate-800 pt-4">
              <Button
                variant="secondary"
                onClick={() => { setSearching(selected); setSelected(null) }}
                data-testid="library-item-search"
              >
                <Search className="h-4 w-4" /> Search for a release
              </Button>
              <Button variant="danger" onClick={() => remove(selected)} disabled={del.isPending}>
                <Trash2 className="h-4 w-4" /> Remove from library
              </Button>
            </div>
          </div>
        )}
      </Modal>

      <InteractiveSearch
        open={searching !== null}
        onClose={() => setSearching(null)}
        title={searching?.title ?? ''}
        platformSlug={searching?.platform_slug}
      />
    </>
  )
}

function Field({ label, value }: { label: string; value?: string }) {
  return (
    <div>
      <dt className="text-xs uppercase tracking-wide text-slate-500">{label}</dt>
      <dd className="mt-0.5 truncate text-slate-200">{value || '—'}</dd>
    </div>
  )
}
