import { NavLink, useLocation } from 'react-router-dom'
import { NAV } from './nav'
import { useDownloads } from '../../api/queries'
import { ACTIVE_STATUSES } from '../ui/StatusPill'

const slugify = (label: string) => label.toLowerCase().replace(/\s+/g, '-')

export function Sidebar({ mobileOpen, onNavigate }: { mobileOpen: boolean; onNavigate: () => void }) {
  const { data: downloads } = useDownloads(15_000)
  const { pathname } = useLocation()
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
        className={`fixed inset-y-0 left-0 z-40 flex w-[210px] flex-col bg-slate-900 transition-transform md:translate-x-0 ${
          mobileOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
      >
        <div className="flex h-[60px] shrink-0 items-center gap-2.5 bg-slate-850 px-5">
          <div className="flex h-8 w-8 items-center justify-center rounded bg-accent-600 text-sm font-black text-white">R</div>
          <span className="text-base font-medium tracking-tight text-white">RomArr</span>
        </div>

        <nav className="flex-1 overflow-y-auto py-2" data-testid="sidebar-nav">
          {NAV.map((item) => {
            const Icon = item.icon
            const badge = item.badge === 'downloads' && activeCount > 0 ? activeCount : null
            const sectionActive = item.end ? pathname === item.to : pathname.startsWith(item.to)
            return (
              <div key={item.to} className={item.divider ? 'mt-3 border-t border-slate-800 pt-3' : undefined}>
                <NavLink
                  to={item.to}
                  end={item.end}
                  onClick={onNavigate}
                  data-testid={`nav-${slugify(item.label)}`}
                  className={({ isActive }) =>
                    `flex items-center gap-2 border-l-[3px] py-3 pl-5 pr-4 text-sm transition-colors ${
                      isActive
                        ? 'border-accent-500 bg-slate-800 text-accent-fg'
                        : 'border-transparent text-slate-200 hover:text-accent-fg'
                    }`
                  }
                >
                  <Icon className="h-[18px] w-[18px]" />
                  <span className="flex-1">{item.label}</span>
                  {badge != null && (
                    <span className="rounded-full bg-accent-600 px-1.5 py-0.5 text-xs font-semibold text-white">{badge}</span>
                  )}
                </NavLink>
                {item.children && sectionActive && (
                  <div className="bg-slate-800/60">
                    {item.children.map((child) => (
                      <NavLink
                        key={child.to}
                        to={child.to}
                        onClick={onNavigate}
                        data-testid={`nav-${slugify(item.label)}-${slugify(child.label)}`}
                        className={({ isActive }) =>
                          `block border-l-[3px] py-2.5 pl-[38px] pr-4 text-sm transition-colors ${
                            isActive
                              ? 'border-accent-500 text-accent-fg'
                              : 'border-transparent text-slate-300 hover:text-accent-fg'
                          }`
                        }
                      >
                        {child.label}
                      </NavLink>
                    ))}
                  </div>
                )}
              </div>
            )
          })}
        </nav>

        <div className="border-t border-slate-800 px-4 py-3 text-xs text-slate-600">RomArr · the *arr for ROMs</div>
      </aside>
    </>
  )
}
