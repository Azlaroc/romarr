import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { setUnauthorizedHandler } from '../api/client'
import { keys, useAuthStatus } from '../api/queries'
import type { AuthStatus } from '../api/types'
import { Login } from './Login'
import { Spinner } from '../components/ui/Spinner'

interface AuthCtx {
  status?: AuthStatus
  refresh: () => void
}
const Ctx = createContext<AuthCtx | null>(null)

export function useAuth(): AuthCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const { data: status, isLoading, isError, refetch } = useAuthStatus()
  const [forceLogin, setForceLogin] = useState(false)

  // Any 401 from a data endpoint means the session is gone (or auth was just
  // enabled) — drop to the login gate.
  useEffect(() => {
    setUnauthorizedHandler(() => setForceLogin(true))
    return () => setUnauthorizedHandler(null)
  }, [])

  const refresh = () => {
    setForceLogin(false)
    qc.invalidateQueries({ queryKey: keys.authStatus })
    refetch()
  }

  if (isLoading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950">
        <Spinner label="Loading RomArr…" />
      </div>
    )
  }

  if (isError || !status) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center gap-3 bg-slate-950 text-slate-300">
        <p>Can’t reach the RomArr server.</p>
        <button onClick={() => refetch()} className="rounded-lg bg-accent-600 px-4 py-2 text-sm font-semibold text-white hover:bg-accent-500">
          Retry
        </button>
      </div>
    )
  }

  const authenticated = status.authenticated && !forceLogin
  const needLogin = forceLogin || (!status.authenticated && (status.has_users || status.oidc_enabled))
  // Open mode (no users, no auth, no OIDC): render the shell directly. If auth
  // turns out to be enforced, the first /api/* 401 flips forceLogin → Login.
  const show = authenticated || !needLogin

  return (
    <Ctx.Provider value={{ status, refresh }}>
      {show ? children : <Login status={status} onAuthenticated={refresh} />}
    </Ctx.Provider>
  )
}
