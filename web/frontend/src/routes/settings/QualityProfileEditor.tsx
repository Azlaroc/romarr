import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { usePlatformRegistry, useQualityProfiles, useSaveQualityProfile } from '../../api/queries'
import { ApiError } from '../../api/client'
import type { QualityProfile } from '../../api/types'
import { REGION_TOKENS, FORMAT_TOKENS } from '../../lib/profileVocab'
import { PageHeader } from '../../components/layout/PageHeader'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { FormRow } from '../../components/ui/FormRow'
import { Input } from '../../components/ui/Input'
import { OrderedTokenList } from '../../components/ui/OrderedTokenList'
import { Skeleton } from '../../components/ui/Skeleton'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

const NEW_PROFILE: QualityProfile = {
  id: 0,
  name: '',
  platform_slug: '',
  is_default: false,
  region_priority: [],
  format_preference: [],
  prefer_1g1r: true,
  allow_proto: false,
  allow_demo: false,
  allow_bios: false,
  source_ranking: [],
  upgrade_allowed: false,
  cutoff_source: '',
}

export function QualityProfileEditor() {
  const params = useParams()
  const isNew = params.id === undefined
  const navigate = useNavigate()
  const { toast } = useToast()

  // No GET-by-id on the backend — hydrate from the list query's cache/fetch.
  const { data: profiles, isSuccess } = useQualityProfiles()
  const save = useSaveQualityProfile()
  const { data: platformRows } = usePlatformRegistry()

  const [form, setForm] = useState<QualityProfile | null>(isNew ? { ...NEW_PROFILE } : null)
  const [nameError, setNameError] = useState('')
  const [apiError, setApiError] = useState('')

  useEffect(() => {
    if (isNew || form != null || !isSuccess) return
    const found = profiles?.find((p) => p.id === Number(params.id))
    if (found) setForm({ ...found })
  }, [isNew, form, isSuccess, profiles, params.id])

  // Which platforms default to this profile — what an operator would orphan
  // by deleting it, read off the registry rather than inferred from a column
  // on the profile.
  const usedBy = (platformRows ?? [])
    .filter((pl) => pl.default_profile_id === Number(params.id))
    .map((pl) => pl.display_name)

  const set = <K extends keyof QualityProfile>(key: K, value: QualityProfile[K]) => {
    setForm((f) => (f ? { ...f, [key]: value } : f))
  }

  // Edit target vanished (bad deep link / deleted elsewhere). PUT to a missing
  // id is a silent 0-row no-op server-side, so offer no save path at all.
  if (!isNew && isSuccess && form == null && !profiles?.some((p) => p.id === Number(params.id))) {
    return (
      <>
        <PageHeader title="Settings" subtitle="Quality profile" />
        <Card>
          <p className="text-sm text-slate-400">Profile not found.</p>
          <Button variant="secondary" size="sm" className="mt-4" onClick={() => navigate('/settings/profiles')}>
            <ArrowLeft className="h-3.5 w-3.5" /> Back to profiles
          </Button>
        </Card>
      </>
    )
  }

  if (form == null) {
    return (
      <>
        <PageHeader title="Settings" subtitle="Quality profile" />
        <Skeleton className="h-64 rounded-xl" />
      </>
    )
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setNameError('')
    setApiError('')

    const name = form.name.trim()
    if (!name) {
      setNameError('Name is required')
      return
    }
    // The backend's name column is UNIQUE but unchecked in the handler (a
    // duplicate surfaces as a 500) — catch it client-side first.
    const dup = profiles?.some((p) => p.id !== form.id && p.name.trim().toLowerCase() === name.toLowerCase())
    if (dup) {
      setNameError('A profile with this name already exists')
      return
    }

    // platform_slug is legacy and no longer written: a profile is free-standing,
    // and which platform defaults to it lives on the platform row.
    const payload: QualityProfile = { ...form, name, platform_slug: '' }
    try {
      await save.mutateAsync(payload)
      toast('Profile saved', 'success')
      navigate('/settings/profiles')
    } catch (err) {
      if (err instanceof ApiError && err.status === 400) {
        setApiError(err.message)
      } else if (err instanceof ApiError && err.status === 400) {
        setApiError(err.message)
      } else if (err instanceof ApiError && err.status === 500) {
        setApiError(`Save failed — the name may already be in use. (${err.message})`)
      } else {
        setApiError('Save failed')
      }
    }
  }

  return (
    <>
      <PageHeader title="Settings" subtitle={isNew ? 'New quality profile' : `Quality profile: ${form.name || '…'}`} />

      <form onSubmit={submit} className="space-y-6">
        <Card title="Profile">
          <FormRow label="Name">
            <Input value={form.name} onChange={(e) => set('name', e.target.value)} error={nameError} placeholder="e.g. PSX — USA, CHD first" data-testid="qp-name" />
          </FormRow>
          <FormRow
            label="Used by"
            hint="A profile is chosen per title when you add one. Which platform defaults to it is set on the Platforms page."
          >
            <div className="text-sm text-slate-400" data-testid="qp-used-by">
              {usedBy.length ? (
                <>
                  Default for <span className="text-slate-200">{usedBy.join(', ')}</span> —{' '}
                  <Link to="/platforms" className="underline decoration-dotted">change on Platforms</Link>
                </>
              ) : (
                <>
                  No platform defaults to this profile.{' '}
                  <Link to="/platforms" className="underline decoration-dotted">Assign it on Platforms</Link>, or pick
                  it per title on the wishlist.
                </>
              )}
            </div>
          </FormRow>
          <FormRow label="Global default">
            <Toggle
              checked={form.is_default}
              onChange={(v) => set('is_default', v)}
              label="Use as the global default"
              hint="Applies to any title whose platform has no default of its own. Turning this on removes the flag from every other profile."
              data-testid="qp-default-toggle"
            />
          </FormRow>
        </Card>

        <Card title="Selection">
          <FormRow label="Region priority" hint="Ordered, best first. Empty = default order (usa › world › europe › japan).">
            <OrderedTokenList value={form.region_priority} onChange={(v) => set('region_priority', v)} available={REGION_TOKENS} testid="qp-region" />
          </FormRow>
          <FormRow label="Format preference" hint="Ordered, best first. Empty = default order (chd › cue › iso › gdi › zip › 7z › raw).">
            <OrderedTokenList value={form.format_preference} onChange={(v) => set('format_preference', v)} available={FORMAT_TOKENS} testid="qp-format" />
          </FormRow>
          <FormRow label="Releases">
            <div className="space-y-3">
              <Toggle checked={form.prefer_1g1r} onChange={(v) => set('prefer_1g1r', v)} label="Prefer 1G1R" hint="One game, one ROM: pick the single best dump per title." data-testid="qp-1g1r" />
              <Toggle checked={form.allow_proto} onChange={(v) => set('allow_proto', v)} label="Allow prototypes / betas / samples" data-testid="qp-allow-proto" />
              <Toggle checked={form.allow_demo} onChange={(v) => set('allow_demo', v)} label="Allow demos" data-testid="qp-allow-demo" />
              <Toggle checked={form.allow_bios} onChange={(v) => set('allow_bios', v)} label="Allow BIOS releases" data-testid="qp-allow-bios" />
            </div>
          </FormRow>
          <FormRow label="Source ranking" hint="Case-insensitive substring match against the indexer name; best first.">
            <OrderedTokenList value={form.source_ranking} onChange={(v) => set('source_ranking', v)} allowCustom testid="qp-source" emptyLabel="None — sources rank equally" />
          </FormRow>
        </Card>

        <Card title="Upgrades — reserved">
          <p className="mb-4 text-xs text-slate-500">Reserved for a future upgrade engine — stored but not read by the selector.</p>
          <div className="space-y-3">
            <Toggle checked={form.upgrade_allowed} onChange={() => undefined} disabled label="Upgrades allowed" data-testid="qp-upgrade-allowed" />
            <Input label="Cutoff source" value={form.cutoff_source} onChange={() => undefined} disabled data-testid="qp-cutoff-source" />
          </div>
        </Card>

        {/* Mounted error region so failures are observable without layout shift. */}
        <p className="min-h-[1rem] text-sm text-red-400" data-testid="qp-error">
          {apiError}
        </p>

        <div className="flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={() => navigate('/settings/profiles')} data-testid="qp-cancel">
            Cancel
          </Button>
          <Button type="submit" disabled={save.isPending} data-testid="qp-save">
            Save profile
          </Button>
        </div>
      </form>
    </>
  )
}
