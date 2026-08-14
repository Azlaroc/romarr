import { Gamepad2, Plus, CalendarDays, Activity, Heart, Joystick, Settings, type LucideIcon } from 'lucide-react'

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

// PR-A information architecture plus the PR-C..F sub-sections. System
// arrives in PR-G.
export const NAV: NavItem[] = [
  { to: '/', label: 'Library', icon: Gamepad2, end: true },
  { to: '/add', label: 'Add New', icon: Plus },
  { to: '/calendar', label: 'Calendar', icon: CalendarDays },
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
  {
    to: '/wanted',
    label: 'Wanted',
    icon: Heart,
    children: [
      { to: '/wanted/wishlist', label: 'Wishlist' },
      { to: '/wanted/requests', label: 'Requests' },
    ],
  },
  { to: '/playlog', label: 'Play Log', icon: Joystick },
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
