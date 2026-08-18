import { Gamepad2, Plus, CalendarDays, Activity, Heart, Joystick, Settings, Wrench, type LucideIcon } from 'lucide-react'

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
  /** Visual divider above this entry (System sits apart, arr-style). */
  divider?: boolean
}

// The full arr information architecture (PR-A shell + PR-C..G sections).
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
      { to: '/settings/media-management', label: 'Media Management' },
      { to: '/settings/profiles', label: 'Profiles' },
      { to: '/settings/quality-definitions', label: 'Quality Definitions' },
      { to: '/settings/indexers', label: 'Indexers' },
      { to: '/settings/download-clients', label: 'Download Clients' },
      { to: '/settings/connect', label: 'Connect' },
      { to: '/settings/metadata', label: 'Metadata' },
      { to: '/settings/tags', label: 'Tags' },
      { to: '/settings/general', label: 'General' },
    ],
  },
  {
    to: '/system',
    label: 'System',
    icon: Wrench,
    divider: true,
    children: [
      { to: '/system/status', label: 'Status' },
      { to: '/system/tasks', label: 'Tasks' },
      { to: '/system/users', label: 'Users' },
      { to: '/system/backup', label: 'Backup' },
    ],
  },
]
