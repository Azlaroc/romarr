import { NavLink } from 'react-router-dom'
import { NAV } from './nav'
import { useDownloads } from '../../api/queries'
import { ACTIVE_STATUSES } from '../ui/StatusPill'

export function Sidebar({ mobileOpen, onNavigate }: { mobileOpen: boolean; onNavigate: () => void }) {
  const { data: downloads } = useDownloads(15_000)
  const activeCount = (downloads ?? []).filter((d) => ACTIVE_STATUSES.includes(d.status)).length

  return (
    <>
      {/* Mobile scrim */}
      <div
        className={`fixed inset-0 z-30 bg-black/60 backdrop-blur-sm transition-opacity md:hidden ${
          mobileOpen ? 'opacity-100' : 'pointer-events-none opacity-0'
        }`}
        onClick={onNavigate}
      />
      <aside
        className={`fixed inset-y-0 left-0 z-40 flex w-56 flex-col border-r border-slate-800 bg-slate-900 transition-transform md:translate-x-0 ${
          mobileOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        <div className="flex h-14 items-center gap-2.5 border-b border-slate-800 px-4">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-accent-600 text-sm font-black text-white">R</div>
          <span className="text-base font-bold tracking-tight text-white">RomArr</span>
        </div>

        <nav className="flex-1 space-y-0.5 overflow-y-auto p-3" data-testid="sidebar-nav">
          {NAV.map((item) => {
            const Icon = item.icon
            const badge = item.badge === 'downloads' && activeCount > 0 ? activeCount : null
            return (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                onClick={onNavigate}
                data-testid={`nav-${item.label.toLowerCase().replace(/\s+/g, '-')}`}
                className={({ isActive }) =>
                  `flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
                    isActive ? 'bg-accent-600/15 text-accent-300' : 'text-slate-400 hover:bg-slate-800 hover:text-slate-100'
                  }`
                }
              >
                <Icon className="h-[18px] w-[18px]" />
                <span className="flex-1">{item.label}</span>
                {badge != null && (
                  <span className="rounded-full bg-accent-600 px-1.5 py-0.5 text-xs font-semibold text-white">{badge}</span>
                )}
              </NavLink>
            )
          })}
        </nav>

        <div className="border-t border-slate-800 px-4 py-3 text-xs text-slate-600">RomArr · the *arr for ROMs</div>
      </aside>
    </>
  )
}
