import { useState, type FormEvent } from 'react'
import { useRegister } from '../api/queries'
import { ApiError } from '../api/client'
import { AuthCard, authInput, authButton } from './AuthCard'

export function Register({ onAuthenticated, onBack }: { onAuthenticated: () => void; onBack?: () => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const register = useRegister()

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    if (password !== confirm) {
      setError('Passwords do not match')
      return
    }
    try {
      const res = await register.mutateAsync({ username: username.trim(), password })
      if (res.success) onAuthenticated()
      else setError(res.error || 'Registration failed')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Registration failed')
    }
  }

  return (
    <AuthCard title="Create admin account" subtitle="First run — set up the owner account">
      <form onSubmit={submit} className="flex flex-col gap-3">
        <input autoFocus value={username} onChange={(e) => setUsername(e.target.value)} placeholder="Username" autoComplete="username" className={authInput} data-testid="register-username" />
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Password" autoComplete="new-password" className={authInput} data-testid="register-password" />
        <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} placeholder="Confirm password" autoComplete="new-password" className={authInput} data-testid="register-confirm" />
        {error && <p className="text-xs text-red-400">{error}</p>}
        <button type="submit" disabled={register.isPending || !username.trim() || !password} className={authButton} data-testid="register-submit">
          {register.isPending ? 'Creating…' : 'Create account'}
        </button>
        {onBack && (
          <button type="button" onClick={onBack} className="text-xs text-slate-500 hover:text-slate-300">
            Back to login
          </button>
        )}
      </form>
    </AuthCard>
  )
}
