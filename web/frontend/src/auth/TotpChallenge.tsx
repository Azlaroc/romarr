import { useState, type FormEvent } from 'react'
import { useLoginTotp } from '../api/queries'
import { ApiError } from '../api/client'
import { AuthCard, authInput, authButton } from './AuthCard'

export function TotpChallenge({
  sessionPending,
  onAuthenticated,
  onCancel,
}: {
  sessionPending: string
  onAuthenticated: () => void
  onCancel: () => void
}) {
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const totp = useLoginTotp()

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    try {
      const res = await totp.mutateAsync({ session_pending: sessionPending, code: code.trim() })
      if (res.success) onAuthenticated()
      else setError(res.error || 'Invalid code')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Verification failed')
    }
  }

  return (
    <AuthCard title="Two-factor" subtitle="Enter the 6-digit code or a backup code">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <input
          autoFocus
          inputMode="numeric"
          autoComplete="one-time-code"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="123456"
          className={`${authInput} text-center tracking-[0.4em]`}
          data-testid="totp-code"
        />
        {error && <p className="text-xs text-red-400">{error}</p>}
        <button type="submit" disabled={totp.isPending || !code.trim()} className={authButton} data-testid="totp-submit">
          {totp.isPending ? 'Verifying…' : 'Verify'}
        </button>
        <button type="button" onClick={onCancel} className="text-xs text-slate-500 hover:text-slate-300">
          Back to login
        </button>
      </form>
    </AuthCard>
  )
}
