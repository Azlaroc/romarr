export function formatSize(bytes?: number): string {
  if (!bytes || bytes <= 0) return ''
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let s = bytes
  while (s >= 1024 && i < units.length - 1) {
    s /= 1024
    i++
  }
  return `${s.toFixed(1)} ${units[i]}`
}

export function formatEta(seconds?: number): string {
  if (!seconds || seconds <= 0 || seconds >= 864000) return ''
  if (seconds > 3600) return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
  if (seconds > 60) return `${Math.floor(seconds / 60)}m`
  return `${Math.floor(seconds)}s`
}

/** Platform label color: PC gets orange, consoles emerald (matches old UI). */
export function platformBadgeColor(isPc?: boolean): 'orange' | 'emerald' {
  return isPc ? 'orange' : 'emerald'
}
