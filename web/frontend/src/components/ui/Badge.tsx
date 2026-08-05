import type { ReactNode } from 'react'

export type BadgeColor = 'slate' | 'accent' | 'emerald' | 'blue' | 'purple' | 'orange' | 'red' | 'yellow'

const colors: Record<BadgeColor, string> = {
  slate: 'bg-slate-700/40 text-slate-300',
  accent: 'bg-accent-500/15 text-accent-300',
  emerald: 'bg-emerald-500/15 text-emerald-400',
  blue: 'bg-blue-500/15 text-blue-400',
  purple: 'bg-purple-500/15 text-purple-400',
  orange: 'bg-orange-500/15 text-orange-400',
  red: 'bg-red-500/15 text-red-400',
  yellow: 'bg-yellow-500/15 text-yellow-400',
}

export function Badge({ color = 'slate', children, className = '' }: { color?: BadgeColor; children: ReactNode; className?: string }) {
  return <span className={`inline-block rounded px-1.5 py-0.5 text-xs font-semibold ${colors[color]} ${className}`}>{children}</span>
}
