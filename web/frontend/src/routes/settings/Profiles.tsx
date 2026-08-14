import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Pencil, Plus, SlidersHorizontal, Trash2 } from 'lucide-react'
import {
  useQualityProfiles,
  useDeleteQualityProfile,
  useReleaseProfiles,
  useDeleteReleaseProfile,
} from '../../api/queries'
import type { QualityProfile, ReleaseProfile } from '../../api/types'
import { PageHeader } from '../../components/layout/PageHeader'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { useToast } from '../../components/ui/Toast'

export function Profiles() {
  const navigate = useNavigate()
  const { toast } = useToast()
  const { data: quality = [] } = useQualityProfiles()
  const { data: release = [] } = useReleaseProfiles()
  const delQuality = useDeleteQualityProfile()
  const delRelease = useDeleteReleaseProfile()

  const [confirmQuality, setConfirmQuality] = useState<QualityProfile | null>(null)
  const [confirmRelease, setConfirmRelease] = useState<ReleaseProfile | null>(null)

  const removeQuality = async () => {
    if (!confirmQuality) return
    try {
      await delQuality.mutateAsync(confirmQuality.id)
      toast('Profile deleted', 'success')
    } catch {
      toast('Failed to delete profile', 'error')
    }
    setConfirmQuality(null)
  }

  const removeRelease = async () => {
    if (!confirmRelease) return
    try {
      await delRelease.mutateAsync(confirmRelease.id)
      toast('Profile deleted', 'success')
    } catch {
      toast('Failed to delete profile', 'error')
    }
    setConfirmRelease(null)
  }

  const regionSummary = (p: QualityProfile) => {
    if (p.region_priority.length === 0) return 'default regions'
    const head = p.region_priority.slice(0, 3).join(' › ')
    const extra = p.region_priority.length - 3
    return extra > 0 ? `${head} +${extra}` : head
  }

  return (
    <>
      <PageHeader title="Settings" subtitle="Profiles" />

      <div className="space-y-6">
        <Card
          title="Quality profiles"
          action={
            <Button size="sm" onClick={() => navigate('/settings/profiles/quality/new')} data-testid="qp-add">
              <Plus className="h-3.5 w-3.5" /> Add
            </Button>
          }
        >
          <p className="mb-3 text-xs text-slate-500">
            Drive the release selector: region priority, format preference, 1G1R and size bounds. A platform profile
            overrides the global default for that platform.
          </p>
          {/* Container stays mounted (empty or full) so tests can observe rows appearing. */}
          <div className="space-y-2" data-testid="profiles-quality-list">
            {quality.map((p) => (
              <div key={p.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-800/40 p-3" data-testid={`qp-row-${p.id}`}>
                <SlidersHorizontal className="h-4 w-4 flex-shrink-0 text-slate-600" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium text-white">{p.name}</span>
                    {p.platform_slug ? <Badge color="accent">{p.platform_slug}</Badge> : <Badge>Global</Badge>}
                    {p.is_default && <Badge color="emerald">Default</Badge>}
                  </div>
                  <div className="mt-0.5 text-xs text-slate-500">{regionSummary(p)}</div>
                </div>
                <Button size="sm" variant="secondary" onClick={() => navigate(`/settings/profiles/quality/${p.id}`)} data-testid={`qp-edit-${p.id}`}>
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </Button>
                <Button size="sm" variant="danger" onClick={() => setConfirmQuality(p)} aria-label={`Delete ${p.name}`} data-testid={`qp-delete-${p.id}`}>
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
            {quality.length === 0 && <p className="py-4 text-center text-sm text-slate-600">No quality profiles.</p>}
          </div>
        </Card>

        <Card
          title="Release profiles"
          action={
            <Button size="sm" onClick={() => navigate('/settings/profiles/release/new')} data-testid="rp-add">
              <Plus className="h-3.5 w-3.5" /> Add
            </Button>
          }
        >
          <p className="mb-3 text-xs text-slate-500">
            Score or reject releases by words in the release title (applied on top of the quality profile).
          </p>
          <div className="space-y-2" data-testid="profiles-release-list">
            {release.map((p) => (
              <div key={p.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-800/40 p-3" data-testid={`rp-row-${p.id}`}>
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium text-white">{p.name}</span>
                    {p.enabled ? <Badge color="emerald">Enabled</Badge> : <Badge>Disabled</Badge>}
                  </div>
                  <div className="mt-0.5 text-xs text-slate-500">
                    {p.must_contain.length} must · {p.must_not_contain.length} must-not · {p.preferred.length} preferred
                  </div>
                </div>
                <Button size="sm" variant="secondary" onClick={() => navigate(`/settings/profiles/release/${p.id}`)} data-testid={`rp-edit-${p.id}`}>
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </Button>
                <Button size="sm" variant="danger" onClick={() => setConfirmRelease(p)} aria-label={`Delete ${p.name}`} data-testid={`rp-delete-${p.id}`}>
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
            {release.length === 0 && <p className="py-4 text-center text-sm text-slate-600">No release profiles.</p>}
          </div>
        </Card>
      </div>

      <ConfirmDialog
        open={confirmQuality != null}
        title="Delete quality profile"
        message={
          <>
            Delete <span className="font-semibold text-white">{confirmQuality?.name}</span>?
            {confirmQuality?.is_default && (
              <span className="mt-2 block text-xs text-yellow-400">
                This is the global default — selection will fall back to the lowest-id global profile, or the built-in
                defaults if none remain.
              </span>
            )}
          </>
        }
        confirmLabel="Delete"
        danger
        busy={delQuality.isPending}
        onConfirm={removeQuality}
        onCancel={() => setConfirmQuality(null)}
      />
      <ConfirmDialog
        open={confirmRelease != null}
        title="Delete release profile"
        message={
          <>
            Delete <span className="font-semibold text-white">{confirmRelease?.name}</span>?
          </>
        }
        confirmLabel="Delete"
        danger
        busy={delRelease.isPending}
        onConfirm={removeRelease}
        onCancel={() => setConfirmRelease(null)}
      />
    </>
  )
}
