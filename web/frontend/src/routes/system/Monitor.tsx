import { Sparkles } from 'lucide-react'
import { useMonitor, useMonitorAction, useTriggerAnalysis } from '../../api/queries'
import { PageHeader } from '../../components/layout/PageHeader'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { EmptyState } from '../../components/ui/EmptyState'
import { useToast } from '../../components/ui/Toast'

const RISK_COLOR: Record<string, 'emerald' | 'yellow' | 'red'> = {
  low: 'emerald',
  medium: 'yellow',
  high: 'red',
}

/** Relocated from Settings (PR-G) + pending-actions queue. */
export function Monitor() {
  const { data } = useMonitor()
  const analyze = useTriggerAnalysis()
  const actions = useMonitorAction()
  const { toast } = useToast()

  const pending = data?.pending_actions ?? []

  const approve = async (id: string) => {
    try {
      const res = await actions.approve.mutateAsync(id)
      toast(res.message ?? (res.success ? 'Action executed' : 'Action failed'), res.success ? 'success' : 'error')
    } catch {
      toast('Approve failed', 'error')
    }
  }

  return (
    <>
      <PageHeader title="System" subtitle="AI monitor" />
      <div className="space-y-6">
        <Card
          title="Monitor"
          action={
            <Button
              size="sm"
              variant="secondary"
              onClick={() => {
                analyze.mutate()
                toast('Analysis triggered', 'success')
              }}
            >
              Analyze now
            </Button>
          }
        >
          <div className="space-y-3" data-testid="monitor-info">
            <div className="flex items-center gap-2">
              <span className={`h-2 w-2 rounded-full ${data?.enabled ? 'bg-emerald-500' : 'bg-slate-600'}`} />
              <span className="text-sm text-slate-300">
                {data?.enabled ? `Active · ${data.provider ?? ''} ${data.model ? `(${data.model})` : ''}` : 'Disabled'}
              </span>
              {data?.auto_fix && <Badge color="yellow">auto-fix</Badge>}
            </div>
            <div className="rounded-lg bg-slate-800 p-3 text-sm text-slate-400">{data?.diagnosis || '—'}</div>
          </div>
        </Card>

        <Card title="Pending actions">
          <div className="space-y-2" data-testid="monitor-actions">
            {pending.map((a) => (
              <div key={a.id} className="flex items-center gap-3 rounded-lg bg-slate-800 p-3" data-testid={`monitor-action-${a.id}`}>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium text-white">{a.action}</span>
                    {a.risk && <Badge color={RISK_COLOR[a.risk] ?? 'slate'}>{a.risk}</Badge>}
                  </div>
                  {a.description && <div className="mt-0.5 text-xs text-slate-500">{a.description}</div>}
                </div>
                <Button size="sm" onClick={() => approve(a.id)} data-testid={`monitor-approve-${a.id}`}>
                  Approve
                </Button>
                <Button size="sm" variant="secondary" onClick={() => actions.dismiss.mutate(a.id)} data-testid={`monitor-dismiss-${a.id}`}>
                  Dismiss
                </Button>
              </div>
            ))}
          </div>
          {pending.length === 0 && (
            <EmptyState icon={Sparkles} title="No pending actions" hint="Approved fixes appear here when the monitor proposes them." />
          )}
        </Card>
      </div>
    </>
  )
}
