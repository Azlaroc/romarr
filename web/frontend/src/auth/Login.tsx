import { useState, type FormEvent } from 'react'
import { LogIn } from 'lucide-react'
import { useLogin } from '../api/queries'
import { ApiError } from '../api/client'
import type { AuthStatus } from '../api/types'
import { AuthCard, authInput, authButton } from './AuthCard'
import { Register } from './Register'
import { TotpChallenge } from './TotpChallenge'

export function Login({ status, onAuthenticated }: { status: AuthStatus; onAuthenticated: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [totpPending, setTotpPending] = useState<string | null>(null)
  const [registering, setRegistering] = useState(false)
  const login = useLogin()

  if (totpPending) {
    return <TotpChallenge sessionPending={totpPending} onAuthenticated={onAuthenticated} onCancel={() => setTotpPending(null)} />
  }
  if (registering || !status.has_users) {
    return <Register onAuthenticated={onAuthenticated} onBack={status.has_users ? () => setRegistering(false) : undefined} />
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    try {
      const res = await login.mutateAsync({ username: username.trim(), password })
      if (res.needs_totp && res.session_pending) {
        setTotpPending(res.session_pending)
      } else if (res.success) {
        onAuthenticated()
      } else {
        setError(res.error || 'Login failed')
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Login failed')
    }
  }

  return (
    <AuthCard title="Sign in to RomArr">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <input autoFocus value={username} onChange={(e) => setUsername(e.target.value)} placeholder="Username" autoComplete="username" className={authInput} data-testid="login-username" />
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Password" autoComplete="current-password" className={authInput} data-testid="login-password" />
        {error && <p className="text-xs text-red-400">{error}</p>}
        <button type="submit" disabled={login.isPending || !username.trim() || !password} className={authButton} data-testid="login-submit">
          <span className="inline-flex items-center justify-center gap-2">
            <LogIn className="h-4 w-4" />
            {login.isPending ? 'Signing in…' : 'Sign in'}
          </span>
        </button>
      </form>

      {status.oidc_enabled && (
        <>
          <div className="my-4 flex items-center gap-3 text-xs text-slate-600">
            <span className="h-px flex-1 bg-slate-800" /> or <span className="h-px flex-1 bg-slate-800" />
          </div>
          <a
            href="/api/oidc/login"
            className="block w-full rounded-lg border border-slate-700 bg-slate-800 px-4 py-2.5 text-center text-sm font-semibold text-slate-200 hover:bg-slate-700"
            data-testid="oidc-login"
          >
            Sign in with {status.oidc_provider || 'SSO'}
          </a>
        </>
      )}
    </AuthCard>
  )
}
