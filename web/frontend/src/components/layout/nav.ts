import { Gamepad2, Plus, Activity, Heart, Settings, type LucideIcon } from 'lucide-react'

export interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  end?: boolean
  /** Dynamic badge source, resolved in the Sidebar. */
  badge?: 'downloads'
}

// PR-A information architecture (the five parity sections). Calendar,
// Requests, History, System, and the Settings sub-pages arrive in PR-B..E.
export const NAV: NavItem[] = [
  { to: '/', label: 'Library', icon: Gamepad2, end: true },
  { to: '/add', label: 'Add New', icon: Plus },
  { to: '/activity', label: 'Activity', icon: Activity, badge: 'downloads' },
  { to: '/wanted', label: 'Wanted', icon: Heart },
  { to: '/settings', label: 'Settings', icon: Settings },
]
