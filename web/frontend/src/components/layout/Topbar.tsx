import { Menu } from 'lucide-react'
import { NotificationsBell } from './NotificationsBell'
import { UserMenu } from './UserMenu'

export function Topbar({ onMenu }: { onMenu: () => void }) {
  return (
    <header className="sticky top-0 z-20 flex h-14 items-center gap-2 border-b border-slate-800 bg-slate-900/80 px-4 backdrop-blur-md sm:px-6">
      <button className="rounded-lg p-2 text-slate-400 hover:bg-slate-800 md:hidden" onClick={onMenu} aria-label="Menu">
        <Menu className="h-5 w-5" />
      </button>
      <div className="flex-1" />
      <NotificationsBell />
      <UserMenu />
    </header>
  )
}
