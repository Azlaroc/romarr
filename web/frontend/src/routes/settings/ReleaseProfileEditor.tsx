import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ArrowLeft, Plus, X } from 'lucide-react'
import { useReleaseProfiles, useSaveReleaseProfile } from '../../api/queries'
import { ApiError } from '../../api/client'
import type { ReleaseProfile } from '../../api/types'
import { PageHeader } from '../../components/layout/PageHeader'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { FormRow } from '../../components/ui/FormRow'
import { Input, inputCls } from '../../components/ui/Input'
import { OrderedTokenList } from '../../components/ui/OrderedTokenList'
import { Skeleton } from '../../components/ui/Skeleton'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

// The backend PUT does not nil-coerce slices — always send real arrays.
const NEW_PROFILE: ReleaseProfile = {
  id: 0,
  name: '',
  must_contain: [],
  must_not_contain: [],
  preferred: [],
  enabled: true,
}

export function ReleaseProfileEditor() {
  const params = useParams()
  const isNew = params.id === undefined
  const navigate = useNavigate()
  const { toast } = useToast()

  const { data: profiles, isSuccess } = useReleaseProfiles()
  const save = useSaveReleaseProfile()

  const [form, setForm] = useState<ReleaseProfile | null>(isNew ? { ...NEW_PROFILE } : null)
  const [nameError, setNameError] = useState('')
  const [apiError, setApiError] = useState('')

  useEffect(() => {
    if (isNew || form != null || !isSuccess) return
    const found = profiles?.find((p) => p.id === Number(params.id))
    if (found) setForm({ ...found, preferred: found.preferred.map((w) => ({ ...w })) })
  }, [isNew, form, isSuccess, profiles, params.id])

  const set = <K extends keyof ReleaseProfile>(key: K, value: ReleaseProfile[K]) => {
    setForm((f) => (f ? { ...f, [key]: value } : f))
  }

  if (!isNew && isSuccess && form == null && !profiles?.some((p) => p.id === Number(params.id))) {
    return (
      <>
        <PageHeader title="Settings" subtitle="Release profile" />
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
        <PageHeader title="Settings" subtitle="Release profile" />
        <Skeleton className="h-64 rounded-xl" />
      </>
    )
  }

  const setPreferred = (i: number, patch: Partial<{ word: string; score: number }>) => {
    set(
      'preferred',
      form.preferred.map((w, idx) => (idx === i ? { ...w, ...patch } : w)),
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
    const dup = profiles?.some((p) => p.id !== form.id && p.name.trim().toLowerCase() === name.toLowerCase())
    if (dup) {
      setNameError('A profile with this name already exists')
      return
    }

    const payload: ReleaseProfile = {
      ...form,
      name,
      preferred: form.preferred.filter((w) => w.word.trim() !== '').map((w) => ({ word: w.word.trim(), score: w.score })),
    }
    try {
      await save.mutateAsync(payload)
      toast('Profile saved', 'success')
      navigate('/settings/profiles')
    } catch (err) {
      if (err instanceof ApiError && err.status === 400) {
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
      <PageHeader title="Settings" subtitle={isNew ? 'New release profile' : `Release profile: ${form.name || '…'}`} />

      <form onSubmit={submit} className="space-y-6">
        <Card title="Profile">
          <FormRow label="Name">
            <Input value={form.name} onChange={(e) => set('name', e.target.value)} error={nameError} placeholder="e.g. Scene words" data-testid="rp-name" />
          </FormRow>
          <FormRow label="Enabled">
            <Toggle checked={form.enabled} onChange={(v) => set('enabled', v)} label="Apply this profile" data-testid="rp-enabled" />
          </FormRow>
        </Card>

        <Card title="Words">
          <FormRow label="Must contain" hint="A release is rejected unless its title contains every word.">
            <OrderedTokenList value={form.must_contain} onChange={(v) => set('must_contain', v)} allowCustom testid="rp-must" emptyLabel="None" />
          </FormRow>
          <FormRow label="Must not contain" hint="A release is rejected if its title contains any word.">
            <OrderedTokenList value={form.must_not_contain} onChange={(v) => set('must_not_contain', v)} allowCustom testid="rp-mustnot" emptyLabel="None" />
          </FormRow>
          <FormRow label="Preferred words" hint="Score adjustment per word found in the title (negative allowed).">
            <div className="space-y-2">
              {form.preferred.map((w, i) => (
                <div key={i} className="flex items-center gap-2">
                  <input
                    value={w.word}
                    onChange={(e) => setPreferred(i, { word: e.target.value })}
                    placeholder="word"
                    className={`${inputCls} flex-1`}
                    data-testid={`rp-preferred-word-${i}`}
                  />
                  <input
                    type="number"
                    value={w.score}
                    onChange={(e) => setPreferred(i, { score: Number(e.target.value) || 0 })}
                    className={`${inputCls} w-24`}
                    data-testid={`rp-preferred-score-${i}`}
                  />
                  <button
                    type="button"
                    onClick={() => set('preferred', form.preferred.filter((_, idx) => idx !== i))}
                    className="text-slate-500 hover:text-red-400"
                    aria-label="Remove preferred word"
                    data-testid={`rp-preferred-remove-${i}`}
                  >
                    <X className="h-4 w-4" />
                  </button>
                </div>
              ))}
              <Button type="button" variant="secondary" size="sm" onClick={() => set('preferred', [...form.preferred, { word: '', score: 0 }])} data-testid="rp-preferred-add">
                <Plus className="h-3.5 w-3.5" /> Add word
              </Button>
            </div>
          </FormRow>
        </Card>

        <p className="min-h-[1rem] text-sm text-red-400" data-testid="rp-error">
          {apiError}
        </p>

        <div className="flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={() => navigate('/settings/profiles')} data-testid="rp-cancel">
            Cancel
          </Button>
          <Button type="submit" disabled={save.isPending} data-testid="rp-save">
            Save profile
          </Button>
        </div>
      </form>
    </>
  )
}
