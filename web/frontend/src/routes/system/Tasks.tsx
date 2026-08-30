import { Play } from 'lucide-react'
import { useRunScheduler, useSchedulerStatus } from '../../api/queries'
import { PageHeader } from '../../components/layout/PageHeader'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { Spinner } from '../../components/ui/Spinner'
import { useToast } from '../../components/ui/Toast'

const MODE_COLOR: Record<string, 'purple' | 'slate'> = { enforce: 'purple', shadow: 'slate' }

export function Tasks() {
  const { data: sched } = useSchedulerStatus()
  const run = useRunScheduler()
  const { toast } = useToast()

  const runNow = async () => {
    try {
      await run.mutateAsync()
      toast('Cycle started', 'success')
    } catch {
      toast('Failed to trigger scheduler', 'error')
    }
  }

  const lastRun = sched?.last_run && !sched.last_run.startsWith('0001-') ? sched.last_run.replace('T', ' ').slice(0, 19) : 'never'

  return (
    <>
      <PageHeader title="System" subtitle="Tasks" />
      <div className="space-y-6">
        <Card
          title="Wishlist scheduler"
          action={
            <Button size="sm" variant="secondary" onClick={runNow} disabled={run.isPending} data-testid="tasks-run-now">
              <Play className="h-3.5 w-3.5" /> Run now
            </Button>
          }
        >
          {sched?.error ? (
            <p className="text-sm text-red-400" data-testid="tasks-scheduler-error">{sched.error}</p>
          ) : (
            <div className="space-y-3" data-testid="tasks-scheduler">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge color={sched?.enabled ? 'emerald' : 'slate'}>{sched?.enabled ? 'Enabled' : 'Disabled'}</Badge>
                <span data-testid="tasks-selector-mode">
                  <Badge color={MODE_COLOR[sched?.selector_mode ?? ''] ?? 'slate'}>
                    selector: {sched?.selector_mode ?? 'off'}
                  </Badge>
                </span>
                {sched?.auto_download && <Badge color="blue">auto-download</Badge>}
                {sched?.running && <Spinner label="running…" />}
              </div>
              <div className="grid grid-cols-2 gap-3 text-xs text-slate-400 sm:grid-cols-4">
                <div>Interval: {sched?.interval_hours ?? '—'}h</div>
                <div>Min score: {sched?.min_score ?? '—'}</div>
                <div>Last run: {lastRun}</div>
                <div>Auto-grabs since start: {sched?.auto_downloads ?? 0}</div>
              </div>
              <p className="text-xs text-slate-600">
                Selector mode is env-driven (SELECTOR_MODE) and read-only here.
              </p>
            </div>
          )}
        </Card>

      </div>
    </>
  )
}
