import { useEffect, useState, type ReactNode } from 'react'

// The one way art is rendered: a lazy <img> that walks a fallback chain on
// error instead of showing the browser's broken-image glyph. `sources` is
// tried in order; when every source has failed (or none was given), the
// placeholder node renders — the accent gradient, never an error state. A
// 404 from the art endpoint is normal ("not minted yet"), so failure here is
// unremarkable by design.
export function ArtImage({
  sources,
  alt = '',
  className = '',
  placeholder,
  testId,
}: {
  sources: string[]
  alt?: string
  className?: string
  placeholder: ReactNode
  testId?: string
}) {
  const [idx, setIdx] = useState(0)
  // A new source list restarts the walk (the same component instance is
  // reused across virtualized cells).
  const key = sources.join('|')
  useEffect(() => setIdx(0), [key])

  const src = sources[idx]
  if (!src) return <>{placeholder}</>
  return (
    <img
      src={src}
      alt={alt}
      loading="lazy"
      className={className}
      data-testid={testId}
      onError={() => setIdx((i) => i + 1)}
    />
  )
}
