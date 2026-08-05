import { Badge, type BadgeColor } from './Badge'

// Maps a download job status to a color + readable label.
const map: Record<string, { color: BadgeColor; label: string }> = {
  queued: { color: 'slate', label: 'Queued' },
  metadata: { color: 'slate', label: 'Metadata' },
  downloading: { color: 'blue', label: 'Downloading' },
  stalled: { color: 'orange', label: 'Stalled' },
  scanning: { color: 'purple', label: 'Scanning' },
  organizing: { color: 'yellow', label: 'Organizing' },
  completed: { color: 'emerald', label: 'Completed' },
  completed_unorganized: { color: 'yellow', label: 'Needs organize' },
  interrupted: { color: 'orange', label: 'Interrupted' },
  error: { color: 'red', label: 'Error' },
  dead_letter: { color: 'red', label: 'Failed' },
}

export function StatusPill({ status }: { status: string }) {
  const s = map[status] ?? { color: 'slate' as BadgeColor, label: status }
  return <Badge color={s.color}>{s.label}</Badge>
}

export const ACTIVE_STATUSES = ['downloading', 'stalled', 'metadata', 'organizing', 'scanning', 'queued']
