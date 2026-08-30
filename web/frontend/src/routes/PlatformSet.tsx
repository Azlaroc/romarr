import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { PageShell } from '../components/layout/PageShell'
import { Badge } from '../components/ui/Badge'
import { Button } from '../components/ui/Button'
import { Card } from '../components/ui/Card'
import { Pagination } from '../components/ui/Pagination'
import { Skeleton } from '../components/ui/Skeleton'
import { usePlatformSet } from '../api/queries'
import type { SetEntry } from '../api/types'

// The set view: a platform's whole catalog reconciled against the disk, every
// group carrying its verdict and the rule that decided it. This screen exists
// because the reference implementations show NOTHING here — a file outside
// the filter either vanishes into one "unmapped" bucket or sits unmarked.
// Every quadrant is a chip; every exclusion names its reason.

const QUADRANTS: { key: string; label: string; hint: string }[] = [
  { key: 'all', label: 'All', hint: 'every catalogued group' },
  { key: 'owned', label: 'Owned', hint: 'the keeper is on disk' },
  { key: 'covered', label: 'Covered', hint: 'an in-profile alternate is on disk — playable, not the preferred dump; the fill leaves these alone' },
  { key: 'gap', label: 'Gaps', hint: 'no dump on disk at all — what collection mode fills' },
  { key: 'out', label: 'Outside profile', hint: 'catalogued, but the collection profile leaves them out; files on disk stay put' },
]

const STATUS_COLOR: Record<string, 'emerald' | 'blue' | 'orange' | 'slate'> = {
  owned: 'emerald',
  covered: 'blue',
  gap: 'orange',
  out: 'slate',
}

function keeperOf(e: SetEntry) {
  return e.keeper_index >= 0 && e.keeper_index < e.members.length ? e.members[e.keeper_index] : null
}

export function PlatformSet() {
  const params = useParams()
  const slug = params.slug ?? ''
  const [status, setStatus] = useState('all')
  const [page, setPage] = useState(1)
  const { data, isLoading } = usePlatformSet(slug, status === 'all' ? '' : status, page)

  const counts = data?.counts
  const chipCount = (key: string) =>
    counts == null
      ? null
      : key === 'all'
        ? counts.groups + counts.out
        : key === 'owned'
          ? counts.owned
          : key === 'covered'
            ? counts.covered
            : key === 'gap'
              ? counts.gaps
              : counts.out

  return (
    <PageShell
      title={`Set — ${slug}`}
      actions={
        <Link to="/platforms">
          <Button variant="secondary" size="sm">
            <ArrowLeft className="h-3.5 w-3.5" /> Platforms
          </Button>
        </Link>
      }
    >
      <div className="space-y-4">
        <Card>
          <div className="flex flex-wrap items-center gap-2 text-xs text-slate-400" data-testid="set-policy">
            {data ? (
              <>
                <span>
                  Profile: <span className="text-slate-200">{data.policy.profile_name}</span>
                </span>
                <span>· regions {data.policy.region_priority.length ? data.policy.region_priority.join(' › ') : 'unordered'}</span>
                {data.policy.verified_only && <Badge color="purple">verified only</Badge>}
                {!data.policy.keep_without_english && <Badge color="orange">English required</Badge>}
                <span>· grouping: {data.grouping}</span>
                <span>
                  · <span className="text-slate-200" data-testid="set-uncatalogued">{data.uncatalogued}</span> files the
                  catalog has never heard of
                </span>
              </>
            ) : (
              <Skeleton className="h-4 w-64" />
            )}
          </div>
        </Card>

        <div className="flex flex-wrap gap-2" data-testid="set-quadrants">
          {QUADRANTS.map((q) => (
            <button
              key={q.key}
              type="button"
              title={q.hint}
              onClick={() => {
                setStatus(q.key)
                setPage(1)
              }}
              data-testid={`set-chip-${q.key}`}
              className={`rounded-full border px-3 py-1 text-xs ${
                status === q.key
                  ? 'border-violet-500 bg-violet-500/20 text-violet-200'
                  : 'border-slate-700 bg-slate-800/60 text-slate-300 hover:bg-slate-800'
              }`}
            >
              {q.label}
              {chipCount(q.key) != null && <span className="ml-1.5 text-slate-500">{chipCount(q.key)}</span>}
            </button>
          ))}
        </div>

        <Card>
          {isLoading && <Skeleton className="h-40 rounded-xl" />}
          <div className="divide-y divide-slate-800" data-testid="set-entries">
            {(data?.entries ?? []).map((e) => {
              const keeper = keeperOf(e)
              const held = e.members.filter((m) => m.owned)
              return (
                <div key={e.key} className="flex items-start gap-3 py-2.5" data-testid={`set-entry`}>
                  <Badge color={STATUS_COLOR[e.status] ?? 'slate'}>{e.status}</Badge>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm text-white">{e.title}</div>
                    <div className="mt-0.5 text-xs text-slate-500">
                      {e.status === 'out'
                        ? `excluded: ${e.reason || e.members[0]?.reason || 'by profile'}${held.length ? ` · ${held.length} file(s) on disk stay put` : ''}`
                        : keeper
                          ? `keeper: ${keeper.name}${keeper.reason ? ` — ${keeper.reason}` : ''}`
                          : 'no keeper'}
                      {e.status === 'covered' && held.length > 0 && ` · holding ${held[0].name}`}
                    </div>
                  </div>
                  <span className="text-xs text-slate-600">{e.members.length} dump{e.members.length === 1 ? '' : 's'}</span>
                </div>
              )
            })}
            {!isLoading && (data?.entries ?? []).length === 0 && (
              <p className="py-6 text-center text-sm text-slate-600">Nothing in this quadrant.</p>
            )}
          </div>
          {data && data.total > data.page_size && (
            <div className="mt-3">
              <Pagination page={page} totalPages={Math.ceil(data.total / data.page_size)} onChange={setPage} />
            </div>
          )}
        </Card>
      </div>
    </PageShell>
  )
}
