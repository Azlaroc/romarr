import { CalendarOff, Heart, Star } from 'lucide-react'
import { useCalendar, useCalendarRecent, useConfig } from '../api/queries'
import type { CalendarEntry } from '../api/types'
import { PageHeader } from '../components/layout/PageHeader'
import { Badge } from '../components/ui/Badge'
import { Card } from '../components/ui/Card'
import { EmptyState } from '../components/ui/EmptyState'
import { Skeleton } from '../components/ui/Skeleton'

/** Group entries by their release date, preserving server order. */
function groupByDate(entries: CalendarEntry[]): [string, CalendarEntry[]][] {
  const groups = new Map<string, CalendarEntry[]>()
  for (const e of entries) {
    const date = e.release_date || 'TBA'
    const bucket = groups.get(date)
    if (bucket) bucket.push(e)
    else groups.set(date, [e])
  }
  return [...groups.entries()]
}

function EntryRow({ e }: { e: CalendarEntry }) {
  return (
    <div className="flex items-center gap-3 rounded-lg border border-slate-800 bg-slate-800/40 px-3 py-2">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate text-sm font-medium text-white">{e.name}</span>
          {e.on_wishlist && (
            <Badge color="accent">
              <Heart className="mr-1 inline h-3 w-3" />
              Wishlisted
            </Badge>
          )}
        </div>
        {(e.platforms?.length ?? 0) > 0 && (
          <div className="mt-0.5 truncate text-xs text-slate-500">{e.platforms!.slice(0, 4).join(' · ')}</div>
        )}
      </div>
      {(e.rating ?? 0) > 0 && (
        <span className="flex flex-shrink-0 items-center gap-1 text-xs text-slate-400">
          <Star className="h-3 w-3 text-yellow-500" />
          {e.rating!.toFixed(1)}
        </span>
      )}
    </div>
  )
}

function DateList({ entries, testid, emptyLabel }: { entries: CalendarEntry[] | undefined; testid: string; emptyLabel: string }) {
  return (
    // Container stays mounted (empty or full) per the SPA convention.
    <div className="space-y-4" data-testid={testid}>
      {entries === undefined ? (
        <Skeleton className="h-24 rounded-lg" />
      ) : entries.length === 0 ? (
        <p className="text-sm text-slate-500">{emptyLabel}</p>
      ) : (
        groupByDate(entries).map(([date, list]) => (
          <div key={date}>
            {/* release_date is a plain "YYYY-MM-DD" string — rendered verbatim. */}
            <div className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-slate-500">{date}</div>
            <div className="space-y-1.5">
              {list.map((e) => (
                <EntryRow key={e.id} e={e} />
              ))}
            </div>
          </div>
        ))
      )}
    </div>
  )
}

export function Calendar() {
  const { data: config, isSuccess: configLoaded } = useConfig()
  const rawgConfigured = config?.rawg?.configured === true
  const { data: upcoming } = useCalendar()
  const { data: recent } = useCalendarRecent()

  return (
    <>
      <PageHeader title="Calendar" subtitle="Release dates via RAWG" />

      {configLoaded && !rawgConfigured ? (
        // Honest empty state: without a RAWG key the backend silently serves
        // empty lists (and caches that for 6h) — say so instead of a blank page.
        <div data-testid="calendar-no-rawg">
          <EmptyState
            icon={CalendarOff}
            title="Calendar needs RAWG metadata"
            hint="Set RAWG_API_KEY on the server to populate upcoming and recent releases."
          />
        </div>
      ) : (
        <div className="space-y-6">
          <Card title="Upcoming releases">
            <DateList entries={upcoming} testid="calendar-upcoming" emptyLabel="No releases in window." />
          </Card>
          <Card title="Recently released">
            <DateList entries={recent} testid="calendar-recent" emptyLabel="No releases in window." />
          </Card>
        </div>
      )}
    </>
  )
}
