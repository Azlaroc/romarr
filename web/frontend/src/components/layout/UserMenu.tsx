import { useState } from 'react'
import { LogOut, User } from 'lucide-react'
import { useAuth } from '../../auth/AuthProvider'
import { useLogout } from '../../api/queries'

export function UserMenu() {
  const { status, refresh } = useAuth()
  const [open, setOpen] = useState(false)
  const logout = useLogout()

  const name = status?.username
  const doLogout = async () => {
    try {
      await logout.mutateAsync()
    } finally {
      setOpen(false)
      refresh()
    }
  }

  return (
    <div className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 rounded-lg px-2 py-1.5 text-sm text-slate-300 hover:bg-slate-800"
        data-testid="user-menu"
      >
        <span className="flex h-7 w-7 items-center justify-center rounded-full bg-slate-700 text-slate-300">
          <User className="h-4 w-4" />
        </span>
        <span className="hidden max-w-[10rem] truncate sm:inline">{name || 'Open access'}</span>
      </button>

      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 z-50 mt-1 w-48 animate-fade-in rounded-lg border border-slate-700 bg-slate-900 p-1 shadow-xl">
            <div className="border-b border-slate-800 px-3 py-2">
              <p className="truncate text-sm font-medium text-white">{name || 'Anonymous'}</p>
              {status?.role && <p className="text-xs text-slate-500">{status.role}</p>}
            </div>
            {name ? (
              <button
                onClick={doLogout}
                className="mt-1 flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm text-slate-300 hover:bg-slate-800"
                data-testid="logout"
              >
                <LogOut className="h-4 w-4" /> Sign out
              </button>
            ) : (
              <p className="px-3 py-2 text-xs text-slate-500">No auth configured</p>
            )}
          </div>
        </>
      )}
    </div>
  )
}
