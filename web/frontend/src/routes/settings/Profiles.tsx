import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Pencil, Plus, SlidersHorizontal, Trash2 } from 'lucide-react'
import {
  usePlatformRegistry,
  useQualityProfiles,
  useDeleteQualityProfile,
  useCollectionProfiles,
  useDeleteCollectionProfile,
  useReleaseProfiles,
  useDeleteReleaseProfile,
} from '../../api/queries'
import type { CollectionProfile, QualityProfile, ReleaseProfile } from '../../api/types'
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
  const { data: platformRows } = usePlatformRegistry()
  const { data: release = [] } = useReleaseProfiles()
  const { data: collection = [] } = useCollectionProfiles()
  const delQuality = useDeleteQualityProfile()
  const delRelease = useDeleteReleaseProfile()
  const delCollection = useDeleteCollectionProfile()

  const [confirmQuality, setConfirmQuality] = useState<QualityProfile | null>(null)
  const [confirmRelease, setConfirmRelease] = useState<ReleaseProfile | null>(null)
  const [confirmCollection, setConfirmCollection] = useState<CollectionProfile | null>(null)

  const removeCollection = async () => {
    if (!confirmCollection) return
    try {
      await delCollection.mutateAsync(confirmCollection.id)
      toast('Profile deleted', 'success')
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Failed to delete profile', 'error')
    }
    setConfirmCollection(null)
  }

  const collectionSummary = (p: CollectionProfile) => {
    const bits: string[] = []
    bits.push(p.region_priority.length ? p.region_priority.slice(0, 3).join(' › ') + (p.region_priority.length > 3 ? ` +${p.region_priority.length - 3}` : '') : 'no region order')
    if (p.verified_only) bits.push('verified only')
    if (!p.keep_without_english) bits.push('English required')
    const allows = [p.allow_proto && 'protos', p.allow_demo && 'demos', p.allow_unlicensed && 'unlicensed', p.allow_aftermarket && 'aftermarket', p.allow_pirate && 'pirate'].filter(Boolean)
    if (allows.length) bits.push('incl. ' + allows.join(', '))
    return bits.join(' · ')
  }

  const followedBy = (id: number) =>
    (platformRows ?? []).filter((pl) => pl.collection_profile_id === id).map((pl) => pl.display_name)

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

  // Which platforms default to a profile, read off the registry — the same
  // answer the delete guard gives, so the screen and the refusal agree.
  const usedBy = (id: number) =>
    (platformRows ?? []).filter((pl) => pl.default_profile_id === id).map((pl) => pl.display_name)

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
            Drive the release selector: region priority, format preference, 1G1R and size bounds. A title is added
            under one — the platform&apos;s default unless you pick another. Templates are cloned for a platform&apos;s
            first title and are never applied directly.
          </p>
          {/* Container stays mounted (empty or full) so tests can observe rows appearing. */}
          <div className="space-y-2" data-testid="profiles-quality-list">
            {quality.map((p) => (
              <div key={p.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-800/40 p-3" data-testid={`qp-row-${p.id}`}>
                <SlidersHorizontal className="h-4 w-4 flex-shrink-0 text-slate-600" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium text-white">{p.name}</span>
                    {p.is_template && <Badge color="orange">Template</Badge>}
                    {p.is_default && <Badge color="emerald">Global default</Badge>}
                  </div>
                  <div className="mt-0.5 text-xs text-slate-500">
                    {regionSummary(p)}
                    {p.is_template
                      ? ` · cloned for new ${p.template_class ?? ''} platforms`
                      : usedBy(p.id).length
                        ? ` · default for ${usedBy(p.id).join(', ')}`
                        : ''}
                  </div>
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
          title="Collection profiles"
          action={
            <Button size="sm" onClick={() => navigate('/settings/profiles/collection/new')} data-testid="cprof-add">
              <Plus className="h-3.5 w-3.5" /> Add
            </Button>
          }
        >
          <p className="mb-3 text-xs text-slate-500">
            What a platform COLLECTS out of its catalog — region order, language, and which dump categories count
            (Retool&apos;s 1G1R vocabulary). Both the collection fill and the declutter follow it; editing one never
            moves a file. Assigned per platform on the Platforms page.
          </p>
          <div className="space-y-2" data-testid="profiles-collection-list">
            {collection.map((p) => (
              <div key={p.id} className="flex items-center gap-3 rounded-xl border border-slate-800 bg-slate-800/40 p-3" data-testid={`cprof-row-${p.id}`}>
                <SlidersHorizontal className="h-4 w-4 flex-shrink-0 text-slate-600" />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium text-white">{p.name}</span>
                    {p.is_default && <Badge color="emerald">Default</Badge>}
                  </div>
                  <div className="mt-0.5 text-xs text-slate-500">
                    {collectionSummary(p)}
                    {followedBy(p.id).length ? ` · followed by ${followedBy(p.id).join(', ')}` : ''}
                  </div>
                </div>
                <Button size="sm" variant="secondary" onClick={() => navigate(`/settings/profiles/collection/${p.id}`)} data-testid={`cprof-edit-${p.id}`}>
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </Button>
                <Button size="sm" variant="danger" onClick={() => setConfirmCollection(p)} disabled={p.is_default} aria-label={`Delete ${p.name}`} data-testid={`cprof-delete-${p.id}`}>
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
            {collection.length === 0 && <p className="py-4 text-center text-sm text-slate-600">No collection profiles.</p>}
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
      <ConfirmDialog
        open={confirmCollection != null}
        title="Delete collection profile"
        message={
          <>
            Delete <span className="font-semibold text-white">{confirmCollection?.name}</span>? A profile a platform
            still follows is refused — re-point the platform first, so a delete can never silently change what it
            collects.
          </>
        }
        confirmLabel="Delete"
        danger
        busy={delCollection.isPending}
        onConfirm={removeCollection}
        onCancel={() => setConfirmCollection(null)}
      />
    </>
  )
}
