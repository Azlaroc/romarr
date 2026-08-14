import { Gamepad2, Plus, Activity, Heart, Settings, type LucideIcon } from 'lucide-react'

export interface NavChild {
  to: string
  label: string
}

export interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  end?: boolean
  /** Dynamic badge source, resolved in the Sidebar. */
  badge?: 'downloads'
  /** Sub-pages, rendered indented while the parent section is active. */
  children?: NavChild[]
}

// PR-A information architecture plus the PR-C sub-sections. Calendar,
// Requests, History, System, and Play Log arrive in PR-D..G.
export const NAV: NavItem[] = [
  { to: '/', label: 'Library', icon: Gamepad2, end: true },
  { to: '/add', label: 'Add New', icon: Plus },
  {
    to: '/activity',
    label: 'Activity',
    icon: Activity,
    badge: 'downloads',
    children: [
      { to: '/activity/queue', label: 'Queue' },
      { to: '/activity/history', label: 'History' },
      { to: '/activity/blocklist', label: 'Blocklist' },
    ],
  },
  { to: '/wanted', label: 'Wanted', icon: Heart },
  {
    to: '/settings',
    label: 'Settings',
    icon: Settings,
    children: [
      { to: '/settings/general', label: 'General' },
      { to: '/settings/profiles', label: 'Profiles' },
    ],
  },
]
