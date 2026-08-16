import { useState } from 'react'
import {
  useSources,
  useTestConnection,
  useTotpDisable,
  useTotpSetup,
  useTotpStatus,
  useTotpVerify,
} from '../../api/queries'
import { isUnauthorized } from '../../api/client'
import { PageHeader } from '../../components/layout/PageHeader'
import { Card } from '../../components/ui/Card'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { Input } from '../../components/ui/Input'
import { useToast } from '../../components/ui/Toast'

const TEST_SERVICES = [
  { id: 'prowlarr', label: 'Prowlarr' },
  { id: 'qbittorrent', label: 'qBittorrent' },
  { id: 'sabnzbd', label: 'SABnzbd' },
  { id: 'nzbget', label: 'NZBGet' },
  { id: 'romm', label: 'RomM' },
]

type TestState = { status: 'idle' | 'testing' | 'ok' | 'fail'; msg?: string }

export function Settings() {
  return (
    <>
      <PageHeader title="Settings" subtitle="General" />
      <div className="space-y-6">
        <ConnectionTests />
        <SearchSources />
        <Security />
      </div>
    </>
  )
}

function ConnectionTests() {
  const test = useTestConnection()
  const [results, setResults] = useState<Record<string, TestState>>({})

  const run = async (service: string) => {
    setResults((r) => ({ ...r, [service]: { status: 'testing' } }))
    try {
      const res = await test.mutateAsync(service)
      setResults((r) => ({
        ...r,
        [service]: res.success ? { status: 'ok', msg: 'Connected' } : { status: 'fail', msg: res.error || 'Failed' },
      }))
    } catch (e) {
      setResults((r) => ({ ...r, [service]: { status: 'fail', msg: e instanceof Error ? e.message : 'Error' } }))
    }
  }

  const statusText = (s?: TestState) => {
    if (!s || s.status === 'idle') return <span className="text-slate-500">Not tested</span>
    if (s.status === 'testing') return <span className="text-yellow-400">Testing…</span>
    if (s.status === 'ok') return <span className="text-emerald-400">{s.msg}</span>
    return <span className="text-red-400">{s.msg}</span>
  }

  return (
    <Card title="Connection tests">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {TEST_SERVICES.map((svc) => (
          <button
            key={svc.id}
            onClick={() => run(svc.id)}
            className="rounded-lg border border-slate-700 bg-slate-800 p-3 text-left transition-colors hover:bg-slate-700"
            data-testid={`test-${svc.id}`}
          >
            <div className="text-sm font-medium text-white">{svc.label}</div>
            <div className="mt-1 text-xs" data-testid={`test-${svc.id}-status`}>{statusText(results[svc.id])}</div>
          </button>
        ))}
      </div>
    </Card>
  )
}

function SearchSources() {
  const { data: sources = [] } = useSources()
  return (
    <Card title="Search sources">
      {sources.length === 0 ? (
        <p className="text-sm text-slate-500">No sources configured.</p>
      ) : (
        <div className="space-y-2" data-testid="settings-sources">
          {sources.map((s, i) => (
            <div key={i} className="flex items-center justify-between rounded-lg bg-slate-800 p-3">
              <div className="flex items-center gap-3">
                <span className={`h-2 w-2 rounded-full ${s.enabled ? 'bg-emerald-500' : 'bg-slate-600'}`} />
                <span className="text-sm text-white">{s.label}</span>
              </div>
              <Badge color={s.source_type === 'torrent' ? 'blue' : 'purple'}>{s.source_type}</Badge>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

/** TOTP self-service — the API requires a real session user (401 in open mode / API-key auth). */
function Security() {
  const { data: status, error } = useTotpStatus()
  const setup = useTotpSetup()
  const verify = useTotpVerify()
  const disable = useTotpDisable()
  const { toast } = useToast()

  const [pending, setPending] = useState<{ secret: string; url: string; backup_codes: string[] } | null>(null)
  const [code, setCode] = useState('')
  const [disabling, setDisabling] = useState(false)
  const [disableCode, setDisableCode] = useState('')

  if (isUnauthorized(error)) {
    return (
      <Card title="Security">
        <p className="text-sm text-slate-500" data-testid="totp-signin-note">
          Sign in with a user account to manage two-factor authentication.
        </p>
      </Card>
    )
  }

  const startSetup = async () => {
    try {
      const res = await setup.mutateAsync()
      setPending({ secret: res.secret, url: res.url, backup_codes: res.backup_codes })
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Setup failed', 'error')
    }
  }

  const confirmVerify = async () => {
    try {
      await verify.mutateAsync(code)
      toast('Two-factor authentication enabled', 'success')
      setPending(null)
      setCode('')
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Invalid code', 'error')
    }
  }

  const confirmDisable = async () => {
    try {
      await disable.mutateAsync(disableCode)
      toast('Two-factor authentication disabled', 'success')
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Invalid code', 'error')
    } finally {
      setDisabling(false)
      setDisableCode('')
    }
  }

  return (
    <Card title="Security">
      <div className="space-y-3" data-testid="totp-card">
        <div className="flex items-center gap-2 text-sm text-slate-300">
          <span className={`h-2 w-2 rounded-full ${status?.enabled ? 'bg-emerald-500' : 'bg-slate-600'}`} />
          Two-factor authentication {status?.enabled ? 'enabled' : 'disabled'}
        </div>

        {!status?.enabled && !pending && (
          <Button size="sm" variant="secondary" onClick={startSetup} disabled={setup.isPending} data-testid="totp-setup">
            Set up TOTP
          </Button>
        )}

        {pending && (
          <div className="space-y-3 rounded-lg border border-slate-800 bg-slate-800/40 p-3">
            <p className="text-xs text-slate-400">
              Add this secret to your authenticator app (or open the otpauth link), then confirm with a code.
            </p>
            <div className="space-y-1 text-xs">
              <div>
                <span className="text-slate-500">Secret: </span>
                <code className="break-all text-accent-fg" data-testid="totp-secret">{pending.secret}</code>
              </div>
              <div>
                <span className="text-slate-500">URI: </span>
                <code className="break-all text-slate-300">{pending.url}</code>
              </div>
              <div>
                <span className="text-slate-500">Backup codes: </span>
                <code className="break-all text-slate-300">{pending.backup_codes.join(' ')}</code>
              </div>
            </div>
            <div className="flex items-end gap-2">
              <Input
                label="Code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder="123456"
                className="max-w-32"
                data-testid="totp-code"
              />
              <Button size="sm" onClick={confirmVerify} disabled={verify.isPending || !code.trim()} data-testid="totp-verify">
                Verify & enable
              </Button>
            </div>
          </div>
        )}

        {status?.enabled && (
          <Button size="sm" variant="danger" onClick={() => setDisabling(true)} data-testid="totp-disable">
            Disable TOTP
          </Button>
        )}
      </div>

      <ConfirmDialog
        open={disabling}
        title="Disable two-factor authentication"
        message={
          <div className="space-y-3">
            <p>Enter a current TOTP code to confirm.</p>
            <Input value={disableCode} onChange={(e) => setDisableCode(e.target.value)} placeholder="123456" data-testid="totp-disable-code" />
          </div>
        }
        confirmLabel="Disable"
        danger
        busy={disable.isPending}
        onConfirm={confirmDisable}
        onCancel={() => {
          setDisabling(false)
          setDisableCode('')
        }}
      />
    </Card>
  )
}
