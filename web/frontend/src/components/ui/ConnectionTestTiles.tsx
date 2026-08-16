import { useState } from 'react'
import { useTestConnection } from '../../api/queries'

export interface TestService {
  id: string
  label: string
  url?: string
  configured?: boolean
}

type TestState = { status: 'idle' | 'testing' | 'ok' | 'fail'; msg?: string }

/** Clickable connection-test tiles (testids test-<id> / test-<id>-status), optionally
 *  showing the configured URL from /api/config under the service name. */
export function ConnectionTestTiles({ services }: { services: TestService[] }) {
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
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {services.map((svc) => (
        <button
          key={svc.id}
          onClick={() => run(svc.id)}
          className="rounded-lg border border-slate-700 bg-slate-800 p-3 text-left transition-colors hover:bg-slate-700"
          data-testid={`test-${svc.id}`}
        >
          <div className="flex items-center gap-2">
            <span className={`h-2 w-2 shrink-0 rounded-full ${svc.configured === false ? 'bg-slate-600' : 'bg-emerald-500'}`} />
            <span className="text-sm font-medium text-white">{svc.label}</span>
          </div>
          {svc.url && <div className="mt-1 truncate text-xs text-slate-500">{svc.url}</div>}
          <div className="mt-1 text-xs" data-testid={`test-${svc.id}-status`}>{statusText(results[svc.id])}</div>
        </button>
      ))}
    </div>
  )
}
