import { useState, type FormEvent } from 'react'
import { Joystick, Star, Trash2 } from 'lucide-react'
import {
  useAddPlayHistory,
  useDeletePlayHistory,
  usePlayHistory,
  usePlayStats,
  useUpdatePlayHistory,
} from '../api/queries'
import { PageHeader } from '../components/layout/PageHeader'
import { PlatformSelect, usePlatformName } from './PlatformSelect'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { EmptyState } from '../components/ui/EmptyState'
import { Input } from '../components/ui/Input'
import { useToast } from '../components/ui/Toast'

function StatTile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-slate-800 bg-slate-800/40 p-3">
      <div className="text-lg font-semibold text-white">{value}</div>
      <div className="text-xs text-slate-500">{label}</div>
    </div>
  )
}

export function PlayLog() {
  const { data } = usePlayHistory()
  const { data: stats } = usePlayStats()
  const add = useAddPlayHistory()
  const update = useUpdatePlayHistory()
  const del = useDeletePlayHistory()
  const platformName = usePlatformName()
  const { toast } = useToast()

  const [title, setTitle] = useState('')
  const [platform, setPlatform] = useState('')
  const [rating, setRating] = useState('')

  const entries = data?.entries ?? []

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const t = title.trim()
    if (!t) {
      toast('Enter a game title', 'error')
      return
    }
    try {
      await add.mutateAsync({
        game_title: t,
        platform: platform ? platformName(platform) : '',
        platform_slug: platform,
        rating: rating ? Number(rating) : undefined,
      })
      toast('Play log entry added', 'success')
      setTitle('')
      setRating('')
    } catch {
      toast('Failed to add entry', 'error')
    }
  }

  const saveRating = async (id: number, value: string, previous?: number) => {
    const n = Number(value)
    if (!value || Number.isNaN(n) || n === (previous ?? 0)) return
    try {
      await update.mutateAsync({ id, fields: { rating: n } })
      toast('Rating updated', 'success')
    } catch {
      toast('Failed to update rating', 'error')
    }
  }

  return (
    <>
      <PageHeader title="Play Log" subtitle="What you've been playing" />

      {stats && (
        <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-5" data-testid="play-stats">
          <StatTile label="Total games" value={String(stats.games_total ?? 0)} />
          <StatTile label="This year" value={String(stats.games_this_year ?? 0)} />
          <StatTile label="This month" value={String(stats.games_this_month ?? 0)} />
          <StatTile label="Avg rating" value={(stats.avg_rating ?? 0).toFixed(1)} />
          <StatTile label="Hours played" value={(stats.total_hours ?? 0).toFixed(1)} />
        </div>
      )}

      <Card title="Log a session" className="mb-6">
        <form onSubmit={submit} className="flex flex-col gap-3 sm:flex-row">
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="Game title"
            className="flex-1"
            data-testid="play-title"
          />
          <PlatformSelect value={platform} onChange={setPlatform} includeAll={false} testid="play-platform" />
          <Input
            type="number"
            min={1}
            max={10}
            value={rating}
            onChange={(e) => setRating(e.target.value)}
            placeholder="Rating 1-10"
            className="sm:w-32"
            data-testid="play-rating"
          />
          <Button type="submit" disabled={add.isPending} data-testid="play-add">
            Add
          </Button>
        </form>
      </Card>

      {/* Container stays mounted when empty so tests can observe rows appearing/shrinking. */}
      <div className="space-y-2" data-testid="playlog-list">
        {entries.map((h) => (
          <div key={h.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-900 p-4" data-testid={`play-row-${h.id}`}>
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium text-white">{h.game_title}</div>
              <div className="mt-0.5 text-xs text-slate-500">
                {h.platform || h.platform_slug || '—'}
                {/* started_at is RFC3339 — date part only, rendered as a substring. */}
                {h.started_at ? ` · ${h.started_at.split('T')[0]}` : ''}
                {(h.hours_played ?? 0) > 0 ? ` · ${h.hours_played}h` : ''}
                {h.notes ? ` · ${h.notes}` : ''}
              </div>
            </div>
            <label className="flex flex-shrink-0 items-center gap-1.5 text-xs text-slate-500">
              <Star className="h-3.5 w-3.5 text-yellow-500" />
              <input
                type="number"
                min={1}
                max={10}
                // Uncontrolled + keyed on the server value so a PATCH refetch resyncs it.
                key={`${h.id}-${h.rating ?? 0}`}
                defaultValue={h.rating || ''}
                onBlur={(e) => saveRating(h.id, e.target.value, h.rating)}
                className="w-14 rounded-lg border border-slate-700 bg-slate-800 px-2 py-1 text-sm text-white focus:border-accent-500 focus:outline-none"
                aria-label={`Rating for ${h.game_title}`}
                data-testid={`play-rating-${h.id}`}
              />
            </label>
            <Button size="sm" variant="danger" onClick={() => del.mutate(h.id)} aria-label={`Delete ${h.game_title}`} data-testid={`play-delete-${h.id}`}>
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        ))}
      </div>
      {entries.length === 0 && (
        <EmptyState icon={Joystick} title="No play sessions logged" hint="Log what you're playing above — stats build up over time." />
      )}
    </>
  )
}
