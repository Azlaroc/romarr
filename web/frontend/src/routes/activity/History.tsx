import { useState } from 'react'
import {
  CalendarClock,
  CheckCircle2,
  Download,
  FlaskConical,
  FolderInput,
  Heart,
  History as HistoryIcon,
  Magnet,
  MailCheck,
  MailPlus,
  RotateCcw,
  Scale,
  Sparkles,
  Upload,
  XCircle,
  type LucideIcon,
} from 'lucide-react'
import { useActivity } from '../../api/queries'
import { parseSelectorDecision } from '../../lib/selectorDecision'
import type { ActivityEntry } from '../../api/types'
import { PageHeader } from '../../components/layout/PageHeader'
import { Badge } from '../../components/ui/Badge'
import { EmptyState } from '../../components/ui/EmptyState'
import { Pagination } from '../../components/ui/Pagination'
import { Spinner } from '../../components/ui/Spinner'

type BadgeColor = 'slate' | 'accent' | 'emerald' | 'blue' | 'purple' | 'orange' | 'red' | 'yellow'

/** Every event_type the backend emits (internal/db LogActivity call sites). */
const EVENT_META: Record<string, { icon: LucideIcon; color: BadgeColor; label: string }> = {
  download_started: { icon: Download, color: 'blue', label: 'Started' },
  download_completed: { icon: CheckCircle2, color: 'emerald', label: 'Downloaded' },
  download_failed: { icon: XCircle, color: 'red', label: 'Failed' },
  download_retried: { icon: RotateCcw, color: 'orange', label: 'Retried' },
  import_completed: { icon: FolderInput, color: 'emerald', label: 'Imported' },
  manual_import: { icon: Upload, color: 'blue', label: 'Manual import' },
  metadata_enriched: { icon: Sparkles, color: 'purple', label: 'Enriched' },
  request_created: { icon: MailPlus, color: 'blue', label: 'Requested' },
  request_completed: { icon: MailCheck, color: 'emerald', label: 'Request done' },
  scheduler_download: { icon: CalendarClock, color: 'accent', label: 'Auto-grabbed' },
  selector_decision: { icon: Scale, color: 'purple', label: 'Selector' },
  wishlist_fulfilled: { icon: Heart, color: 'emerald', label: 'Fulfilled' },
  torrent_harvested: { icon: Magnet, color: 'blue', label: 'Harvested' },
  test: { icon: FlaskConical, color: 'slate', label: 'Test' },
}

const PAGE_SIZE = 50 // fixed server-side

function SelectorDetail({ detail }: { detail: string }) {
  const d = parseSelectorDecision(detail)
  // On regex miss render the raw detail — never hide it.
  if (!d) return <span className="text-slate-500">{detail}</span>
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      <Badge color={d.mode === 'enforce' ? 'accent' : 'slate'}>{d.mode}</Badge>
      <Badge color={d.action.startsWith('grab') ? 'emerald' : 'slate'}>{d.action}</Badge>
      <span className="text-slate-500">{d.rest}</span>
    </span>
  )
}

function Row({ e }: { e: ActivityEntry }) {
  const meta = EVENT_META[e.event_type]
  const Icon = meta?.icon ?? FlaskConical
  // Raw string date — timestamps are server-formatted text, never new Date().
  const when = (e.timestamp ?? '').replace('T', ' ').slice(0, 16)
  return (
    <div className="flex items-start gap-3 rounded-xl border border-slate-800 bg-slate-900 p-3" data-testid={`history-row-${e.id ?? ''}`}>
      <Icon className="mt-0.5 h-4 w-4 flex-shrink-0 text-slate-500" />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <Badge color={meta?.color ?? 'slate'}>{meta?.label ?? e.event_type}</Badge>
          <span className="break-words text-sm font-medium text-white">{e.title}</span>
        </div>
        {e.detail && (
          <div className="mt-1 text-xs">
            {e.event_type === 'selector_decision' ? <SelectorDetail detail={e.detail} /> : <span className="text-slate-500">{e.detail}</span>}
          </div>
        )}
      </div>
      <span className="flex-shrink-0 text-xs text-slate-600">{when}</span>
    </div>
  )
}

export function History() {
  const [page, setPage] = useState(1)
  const { data, isLoading } = useActivity(page)
  const entries = data?.entries ?? []
  const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / PAGE_SIZE))

  return (
    <>
      <PageHeader title="Activity" subtitle="History" />

      {isLoading ? (
        <div className="py-16">
          <Spinner label="Loading history…" className="justify-center" />
        </div>
      ) : (
        <>
          {/* Container stays mounted (empty or full) so tests can observe rows appearing. */}
          <div className="space-y-2" data-testid="history-list">
            {entries.map((e, i) => (
              <Row key={e.id ?? i} e={e} />
            ))}
          </div>
          {entries.length === 0 && <EmptyState icon={HistoryIcon} title="No activity yet" hint="Grabs, imports, and selector decisions land here." />}
          <div className="mt-4">
            <Pagination page={page} totalPages={totalPages} onChange={setPage} />
          </div>
        </>
      )}
    </>
  )
}
