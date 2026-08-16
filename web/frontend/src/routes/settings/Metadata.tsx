import { PageHeader } from '../../components/layout/PageHeader'
import { Badge } from '../../components/ui/Badge'
import { Card } from '../../components/ui/Card'
import { Toggle } from '../../components/ui/Toggle'

export function Metadata() {
  return (
    <>
      <PageHeader title="Settings" subtitle="Metadata" />
      <div className="space-y-6">
        <Card title="Sources">
          <div className="space-y-2" data-testid="md-sources">
            <div className="flex items-center justify-between gap-3 rounded bg-slate-800 p-3">
              <div className="min-w-0">
                <div className="text-sm text-white">RomM</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  Titles, artwork, and platform metadata come from the RomM library it syncs against
                </div>
              </div>
              <Badge color="emerald">library of record</Badge>
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
