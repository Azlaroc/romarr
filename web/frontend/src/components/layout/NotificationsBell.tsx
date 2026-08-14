import { useEffect, useState } from 'react'
import { Bell, CheckCheck } from 'lucide-react'
import {
  useMarkAllNotificationsRead,
  useMarkNotificationRead,
  useNotifications,
  useUnreadCount,
} from '../../api/queries'
import { Spinner } from '../ui/Spinner'

export function NotificationsBell() {
  const [open, setOpen] = useState(false)
  const { data: count } = useUnreadCount()
  const { data: items, isLoading } = useNotifications(open)
  const markRead = useMarkNotificationRead()
  const markAll = useMarkAllNotificationsRead()
  const unread = count ?? 0

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  return (
    <div className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        className="relative rounded-lg p-2 text-slate-400 hover:bg-slate-800 hover:text-slate-100"
        title="Notifications"
        aria-label="Notifications"
        data-testid="notifications-bell"
      >
        <Bell className="h-5 w-5" />
        {unread > 0 && (
          <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-accent-600 px-1 text-[10px] font-bold text-white">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>

      {open && (
        <>
          {/* Outside-click closer (same pattern as UserMenu — no portal, no deps). */}
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div
            className="absolute right-0 z-50 mt-1 max-h-96 w-80 overflow-y-auto rounded-lg border border-slate-700 bg-slate-900 shadow-xl animate-fade-in"
            data-testid="bell-panel"
          >
            <div className="sticky top-0 flex items-center justify-between border-b border-slate-800 bg-slate-900 px-3 py-2">
              <span className="text-sm font-medium text-white">Notifications</span>
              <button
                onClick={() => markAll.mutate()}
                className="flex items-center gap-1 rounded-md px-2 py-1 text-xs text-slate-400 hover:bg-slate-800 hover:text-slate-100"
                data-testid="bell-read-all"
              >
                <CheckCheck className="h-3.5 w-3.5" /> Mark all read
              </button>
            </div>

            {isLoading ? (
              <div className="p-4">
                <Spinner label="Loading…" />
              </div>
            ) : (items ?? []).length === 0 ? (
              <p className="p-4 text-sm text-slate-500">No notifications</p>
            ) : (
              <div className="p-1">
                {(items ?? []).map((n, i) => (
                  <button
                    key={n.id}
                    onClick={() => !n.read && markRead.mutate(n.id)}
                    className="flex w-full items-start gap-2 rounded-md px-3 py-2 text-left hover:bg-slate-800"
                    data-testid={`bell-item-${i}`}
                  >
                    <span
                      className={`mt-1.5 h-2 w-2 flex-shrink-0 rounded-full ${n.read ? 'bg-slate-700' : 'bg-accent-500'}`}
                      aria-hidden
                    />
                    <span className="min-w-0">
                      <span className="block truncate text-sm text-slate-200">{n.title}</span>
                      {n.message && <span className="block text-xs text-slate-500">{n.message}</span>}
                      {n.created_at && (
                        // RFC3339 — render a readable substring, no Date parsing.
                        <span className="block text-[11px] text-slate-600">{n.created_at.replace('T', ' ').slice(0, 16)}</span>
                      )}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  )
}
