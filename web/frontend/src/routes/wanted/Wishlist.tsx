import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { Heart, Search, Trash2 } from 'lucide-react'
import { useWishlist, useAddWishlist, useDeleteWishlist } from '../../api/queries'
import { PageHeader } from '../../components/layout/PageHeader'
import { PlatformSelect, usePlatformName } from '../PlatformSelect'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { EmptyState } from '../../components/ui/EmptyState'
import { useToast } from '../../components/ui/Toast'

const inputCls =
  'w-full rounded-lg border border-slate-700 bg-slate-800 px-4 py-2 text-sm text-white placeholder-slate-500 focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500'

export function Wishlist() {
  const { data: items = [] } = useWishlist()
  const add = useAddWishlist()
  const del = useDeleteWishlist()
  const platformName = usePlatformName()
  const navigate = useNavigate()
  const { toast } = useToast()

  const [title, setTitle] = useState('')
  const [platform, setPlatform] = useState('')

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

      {items.length === 0 ? (
        <EmptyState icon={Heart} title="Wishlist is empty" hint="Add a title above; the scheduler will auto-search for it." />
      ) : (
        <div className="space-y-2" data-testid="wishlist">
          {items.map((w) => (
            <div key={w.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-900 p-4">
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium text-white">{w.title}</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  {(w.platform || w.platform_slug || '—')}
                  {w.added_at ? ` · ${w.added_at.split('T')[0]}` : ''}
                </div>
              </div>
              <Button size="sm" variant="secondary" onClick={() => navigate(`/add?q=${encodeURIComponent(w.title)}`)}>
                <Search className="h-3.5 w-3.5" /> Search
              </Button>
              <Button size="sm" variant="danger" onClick={() => del.mutate(w.id)} aria-label="Delete" data-testid="wish-delete">
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          ))}
        </div>
      )}
    </>
  )
}
