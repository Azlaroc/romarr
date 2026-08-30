import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { useCollectionProfiles, usePlatformRegistry, useSaveCollectionProfile } from '../../api/queries'
import { ApiError } from '../../api/client'
import type { CollectionProfile } from '../../api/types'
import { REGION_TOKENS } from '../../lib/profileVocab'
import { PageHeader } from '../../components/layout/PageHeader'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { FormRow } from '../../components/ui/FormRow'
import { Input } from '../../components/ui/Input'
import { OrderedTokenList } from '../../components/ui/OrderedTokenList'
import { Skeleton } from '../../components/ui/Skeleton'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

// A collection profile is the platform's declared slice of its DAT — what it
// COLLECTS (regions, languages, categories), in Retool's 1G1R vocabulary.
// Quality profiles keep the release-side concerns (format, source). Both the
// fill and the declutter read this object; editing it never moves a file.

const NEW_PROFILE: CollectionProfile = {
  id: 0,
  is_default: false,
  name: '',
  region_priority: [],
  english_preferred: true,
  keep_without_english: true,
  allow_proto: false,
  allow_demo: false,
  allow_bios: false,
  allow_unlicensed: false,
  allow_aftermarket: false,
  allow_pirate: false,
  verified_only: false,
  exclude_categories: [],
}

export function CollectionProfileEditor() {
  const params = useParams()
  const isNew = params.id === undefined
  const navigate = useNavigate()
  const { toast } = useToast()

  const { data: profiles, isSuccess } = useCollectionProfiles()
  const save = useSaveCollectionProfile()
  const { data: platformRows } = usePlatformRegistry()

  const [form, setForm] = useState<CollectionProfile | null>(isNew ? { ...NEW_PROFILE } : null)
  const [nameError, setNameError] = useState('')
  const [apiError, setApiError] = useState('')

  useEffect(() => {
    if (isNew || form != null || !isSuccess) return
    const found = profiles?.find((p) => p.id === Number(params.id))
    if (found) setForm({ ...found })
  }, [isNew, form, isSuccess, profiles, params.id])

  const usedBy = (platformRows ?? [])
    .filter((pl) => pl.collection_profile_id === Number(params.id))
    .map((pl) => pl.display_name)

  const set = <K extends keyof CollectionProfile>(key: K, value: CollectionProfile[K]) => {
    setForm((f) => (f ? { ...f, [key]: value } : f))
  }

  if (!isNew && isSuccess && form == null && !profiles?.some((p) => p.id === Number(params.id))) {
    return (
      <>
        <PageHeader title="Settings" subtitle="Collection profile" />
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
        <PageHeader title="Settings" subtitle="Collection profile" />
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
    try {
      await save.mutateAsync({ ...form, name })
      toast('Collection profile saved', 'success')
      navigate('/settings/profiles')
    } catch (err) {
      setApiError(err instanceof ApiError ? err.message : 'Save failed')
    }
  }

  return (
    <>
      <PageHeader
        title="Settings"
        subtitle={isNew ? 'New collection profile' : `Collection profile: ${form.name || '…'}`}
      />

      <form onSubmit={submit} className="space-y-6">
        <Card title="Profile">
          <FormRow label="Name">
            <Input value={form.name} onChange={(e) => set('name', e.target.value)} error={nameError} placeholder="e.g. Licensed Retail — English preferred" data-testid="cprof-name" />
          </FormRow>
          <FormRow
            label="Used by"
            hint="Assigned per platform on the Platforms page. Unassigned platforms follow the default profile."
          >
            <div className="text-sm text-slate-400" data-testid="cprof-used-by">
              {form.is_default ? (
                <>The default — every platform without its own assignment follows this profile.</>
              ) : usedBy.length ? (
                <>
                  Followed by <span className="text-slate-200">{usedBy.join(', ')}</span> —{' '}
                  <Link to="/platforms" className="underline decoration-dotted">change on Platforms</Link>
                </>
              ) : (
                <>
                  No platform follows this profile yet.{' '}
                  <Link to="/platforms" className="underline decoration-dotted">Assign it on Platforms</Link>.
                </>
              )}
            </div>
          </FormRow>
        </Card>

        <Card title="Regions & language">
          <FormRow label="Region priority" hint="Ordered, best first — it decides which dump is the keeper. It never drops a game: a Japan-only title keeps its Japanese dump under any order.">
            <OrderedTokenList value={form.region_priority} onChange={(v) => set('region_priority', v)} available={REGION_TOKENS} testid="cprof-region" />
          </FormRow>
          <FormRow label="Language">
            <div className="space-y-3">
              <Toggle checked={form.english_preferred} onChange={(v) => set('english_preferred', v)} label="Prefer English releases" hint="An English-tagged dump wins keeper ties." data-testid="cprof-english" />
              <Toggle checked={form.keep_without_english} onChange={(v) => set('keep_without_english', v)} label="Keep games with no English release" hint="Off drops Japan-only orphans from the set entirely — Retool's strict-English mode." data-testid="cprof-keep-orphans" />
            </div>
          </FormRow>
        </Card>

        <Card title="What counts as the collection">
          <p className="mb-4 text-xs text-slate-500">
            Retool's exclusion vocabulary. Off = those dumps classify as outside the profile: visible,
            never wanted, never touched on disk.
          </p>
          <FormRow label="Dump nature">
            <div className="space-y-3">
              <Toggle checked={form.allow_proto} onChange={(v) => set('allow_proto', v)} label="Prototypes / betas / samples" data-testid="cprof-allow-proto" />
              <Toggle checked={form.allow_demo} onChange={(v) => set('allow_demo', v)} label="Demos & kiosks" data-testid="cprof-allow-demo" />
              <Toggle checked={form.allow_bios} onChange={(v) => set('allow_bios', v)} label="BIOS images" data-testid="cprof-allow-bios" />
            </div>
          </FormRow>
          <FormRow label="Licensing">
            <div className="space-y-3">
              <Toggle checked={form.allow_unlicensed} onChange={(v) => set('allow_unlicensed', v)} label="Unlicensed (Unl)" data-testid="cprof-allow-unl" />
              <Toggle checked={form.allow_aftermarket} onChange={(v) => set('allow_aftermarket', v)} label="Aftermarket homebrew" data-testid="cprof-allow-aftermarket" />
              <Toggle checked={form.allow_pirate} onChange={(v) => set('allow_pirate', v)} label="Pirate / bootleg" data-testid="cprof-allow-pirate" />
            </div>
          </FormRow>
          <FormRow label="Verification">
            <Toggle checked={form.verified_only} onChange={(v) => set('verified_only', v)} label="Verified dumps only" hint="Narrows the set to dumps the authority marks verified." data-testid="cprof-verified-only" />
          </FormRow>
          <FormRow label="Excluded categories" hint="Clone-list categories left out of the set (Applications, Educational…). Free text — the vocabulary is upstream's.">
            <OrderedTokenList value={form.exclude_categories} onChange={(v) => set('exclude_categories', v)} allowCustom testid="cprof-categories" emptyLabel="None excluded" />
          </FormRow>
        </Card>

        <p className="min-h-[1rem] text-sm text-red-400" data-testid="cprof-error">
          {apiError}
        </p>

        <div className="flex gap-3">
          <Button type="submit" disabled={save.isPending} data-testid="cprof-save">
            Save profile
          </Button>
          <Button type="button" variant="secondary" onClick={() => navigate('/settings/profiles')} data-testid="cprof-cancel">
            Cancel
          </Button>
        </div>
      </form>
    </>
  )
}
