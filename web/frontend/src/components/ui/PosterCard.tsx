import type { ReactNode } from 'react'
import { ArtImage } from './ArtImage'

// The browse card, both levels: `photo` (16:10 — platform tiles, hardware
// shots float on the accent gradient) and `poster` (3:4 — game covers fill
// the frame). Art always sits ON the accent gradient, so a missing or
// still-minting image degrades to the same look the art fades in over —
// there is no broken state, only a quieter one.
export function PosterCard({
  variant,
  title,
  accent,
  artSources,
  artFit,
  chips,
  badge,
  subtitle,
  onClick,
  testId,
}: {
  variant: 'poster' | 'photo'
  title: string
  /** #rrggbb; empty = neutral slate gradient. */
  accent?: string
  artSources: string[]
  /** contain floats a transparent hardware shot; cover fills with box art. */
  artFit?: 'contain' | 'cover'
  chips?: ReactNode
  badge?: ReactNode
  subtitle?: string
  onClick?: () => void
  testId?: string
}) {
  const aspect = variant === 'photo' ? 'aspect-[16/10]' : 'aspect-[3/4]'
  const fit = artFit ?? (variant === 'photo' ? 'contain' : 'cover')
  const base = accent || '#334155'
  const gradient = { background: `linear-gradient(160deg, ${base}33 0%, ${base}18 45%, #0f172a 100%)` }

  const placeholder = (
    <div className="flex h-full w-full items-center justify-center">
      <span className="text-4xl font-bold" style={{ color: `${base}66` }}>
        {(title || '?').slice(0, 1).toUpperCase()}
      </span>
    </div>
  )

  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={testId}
      className="group w-full overflow-hidden rounded-xl border border-slate-800 bg-slate-900 text-left transition-colors hover:border-slate-600"
    >
      <div className={`relative w-full overflow-hidden ${aspect}`} style={gradient}>
        <ArtImage
          sources={artSources}
          placeholder={placeholder}
          className={`h-full w-full ${fit === 'contain' ? 'object-contain p-3' : 'object-cover'}`}
        />
        {badge && <div className="absolute right-2 top-2">{badge}</div>}
      </div>
      <div className="px-3 py-2">
        <p className="truncate text-sm font-medium text-slate-200" title={title}>
          {title}
        </p>
        {subtitle && <p className="truncate text-xs text-slate-500">{subtitle}</p>}
        {chips && <div className="mt-1.5 flex flex-wrap items-center gap-1">{chips}</div>}
      </div>
    </button>
  )
}
