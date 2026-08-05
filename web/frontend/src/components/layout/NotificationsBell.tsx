import { Bell } from 'lucide-react'
import { useUnreadCount } from '../../api/queries'

export function NotificationsBell() {
  const { data: count } = useUnreadCount()
  const unread = count ?? 0
  return (
    <button
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
  )
}
