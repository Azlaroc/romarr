import { useConfig } from '../../api/queries'
import { PageHeader } from '../../components/layout/PageHeader'
import { Badge } from '../../components/ui/Badge'
import { Card } from '../../components/ui/Card'
import { Toggle } from '../../components/ui/Toggle'

export function Metadata() {
  const { data: config } = useConfig()
  const rawgConfigured = config?.rawg?.configured === true

  return (
    <>
      <PageHeader title="Settings" subtitle="Metadata" />
      <div className="space-y-6">
        <Card title="Sources">
          <div className="space-y-2" data-testid="md-sources">
            <div className="flex items-center justify-between gap-3 rounded-lg bg-slate-800 p-3">
              <div className="min-w-0">
                <div className="text-sm text-white">RomM</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  Titles, artwork, and platform metadata come from the RomM library it syncs against
                </div>
              </div>
              <Badge color="emerald">library of record</Badge>
            </div>
            <div className="flex items-center justify-between gap-3 rounded-lg bg-slate-800 p-3">
              <div className="min-w-0">
                <div className="text-sm text-white">RAWG</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  Release-date and artwork enrichment for the calendar; API key is set by the container environment
                </div>
              </div>
              <Badge color={rawgConfigured ? 'emerald' : 'slate'}>{rawgConfigured ? 'configured' : 'not configured'}</Badge>
            </div>
          </div>
        </Card>

        <Card title="Metadata enrichment — reserved">
          <p className="mb-4 text-xs text-slate-500">
            Reserved for per-source enrichment controls (provider order, per-platform preferences). Nothing here is
            configurable yet — enrichment runs with built-in behavior.
          </p>
          <Toggle checked={false} onChange={() => undefined} disabled label="Custom enrichment rules" hint="Not implemented" data-testid="md-enrichment-reserved" />
        </Card>
      </div>
    </>
  )
}
