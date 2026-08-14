import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { Heart, Search, Trash2 } from 'lucide-react'
import { useWishlist, useAddWishlist, useDeleteWishlist, useActivity, useQualityProfiles } from '../../api/queries'
import type { ActivityEntry, WishlistItem } from '../../api/types'
import { parseSelectorDecision } from '../../lib/selectorDecision'
import { PageHeader } from '../../components/layout/PageHeader'
import { PlatformSelect, usePlatformName } from '../PlatformSelect'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { EmptyState } from '../../components/ui/EmptyState'
import { useToast } from '../../components/ui/Toast'

const inputCls =
  'w-full rounded-lg border border-slate-700 bg-slate-800 px-4 py-2 text-sm text-white placeholder-slate-500 focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500'

const SELECTOR_EVENTS = ['selector_decision', 'scheduler_download', 'wishlist_fulfilled']

interface ChipState {
  label: string
  color: 'slate' | 'accent' | 'emerald'
  reason?: string
  raw?: string
}

/** Newest selector-flavored activity entry for this title (entries arrive newest-first). */
function chipFor(item: WishlistItem, entries: ActivityEntry[]): ChipState {
  const t = item.title.trim().toLowerCase()
  const e = entries.find(
    (en) => SELECTOR_EVENTS.includes(en.event_type) && en.title.trim().toLowerCase() === t,
  )
  if (!e) return { label: 'Waiting for next cycle', color: 'slate' }
  if (e.event_type === 'scheduler_download') return { label: 'Grabbed', color: 'accent', raw: e.detail }
  if (e.event_type === 'wishlist_fulfilled') return { label: 'Owned', color: 'emerald', raw: e.detail }
  const d = parseSelectorDecision(e.detail ?? '')
  if (d?.action.startsWith('grab')) return { label: 'Grabbed', color: 'accent', raw: e.detail }
  if (d) return { label: 'Skipped', color: 'slate', reason: d.rest, raw: e.detail }
  return { label: 'Decision', color: 'slate', raw: e.detail }
}

export function Wishlist() {
  const { data: items = [] } = useWishlist()
  const { data: activity } = useActivity(1)
  const { data: profiles } = useQualityProfiles()
  const add = useAddWishlist()
  const del = useDeleteWishlist()
  const platformName = usePlatformName()
  const navigate = useNavigate()
  const { toast } = useToast()

  const [title, setTitle] = useState('')
  const [platform, setPlatform] = useState('')

  // Mirrors the backend's ResolveQualityProfile chain:
  // platform exact -> is_default global -> lowest-id global -> built-in.
  const profileFor = (slug?: string): string => {
    const list = profiles ?? []
    if (slug) {
      const exact = list.find((p) => p.platform_slug === slug)
      if (exact) return exact.name
    }
    const globals = list.filter((p) => p.platform_slug === '')
    const def = globals.find((p) => p.is_default)
    if (def) return def.name
    const lowest = [...globals].sort((a, b) => a.id - b.id)[0]
    return lowest ? lowest.name : 'built-in defaults'
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const t = title.trim()
    if (!t) {
      toast('Title required', 'error')
      return
    }
    try {
      await add.mutateAsync({ title: t, platform: platform ? platformName(platform) : '', platform_slug: platform })
      setTitle('')
      toast('Added to wishlist', 'success')
    } catch {
      toast('Failed to add', 'error')
    }
  }

  const entries = activity?.entries ?? []

  return (
    <>
      <PageHeader title="Wanted" subtitle="Wishlist" />

      <Card title="Add to wishlist" className="mb-6">
        <form onSubmit={submit} className="flex flex-col gap-3 sm:flex-row">
          <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Game title" className={`${inputCls} flex-1`} data-testid="wish-title" />
          <PlatformSelect value={platform} onChange={setPlatform} includeAll={false} testid="wish-platform" />
          <Button type="submit" disabled={add.isPending || !title.trim()} data-testid="wish-add">Add</Button>
        </form>
      </Card>

      {/* Container stays mounted when empty so the list can be observed shrinking to zero. */}
      <div className="space-y-2" data-testid="wishlist">
        {items.map((w) => {
          const chip = chipFor(w, entries)
          return (
            <div key={w.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-900 p-4">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-sm font-medium text-white">{w.title}</span>
                  <span data-testid={`wish-status-${w.id}`} title={chip.raw}>
                    <Badge color={chip.color}>{chip.label}</Badge>
                  </span>
                </div>
                <div className="mt-0.5 text-xs text-slate-500">
                  {(w.platform || w.platform_slug || '—')}
                  {w.added_at ? ` · ${w.added_at.split('T')[0]}` : ''}
                  {` · auto-search runs under: ${profileFor(w.platform_slug)}`}
                </div>
                {chip.reason && <div className="mt-0.5 truncate text-xs text-slate-600">{chip.reason}</div>}
              </div>
              <Button size="sm" variant="secondary" onClick={() => navigate(`/add?q=${encodeURIComponent(w.title)}`)}>
                <Search className="h-3.5 w-3.5" /> Search
              </Button>
              <Button size="sm" variant="danger" onClick={() => del.mutate(w.id)} aria-label="Delete" data-testid="wish-delete">
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          )
        })}
      </div>
      {items.length > 0 && (
        <p className="mt-2 text-xs text-slate-600">
          Selector status reflects recent activity only (latest page); it is not a live view.
        </p>
      )}
      {items.length === 0 && (
        <EmptyState icon={Heart} title="Wishlist is empty" hint="Add a title above; the scheduler will auto-search for it." />
      )}
    </>
  )
}
