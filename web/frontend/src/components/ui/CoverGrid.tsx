import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { useWindowVirtualizer } from '@tanstack/react-virtual'
import { columnsFor, itemRange, jumpScrollOffset, rowCount, rowOfOffset } from '../../lib/coverGridMath'

// The windowed grid behind every browse level — the survey law this app
// builds under: virtualize from day one (the reference implementation that
// shipped without it died at exactly this library's scale). Rows virtualize
// against the window scroll; columns respond to container width; unloaded
// indices render the caller's placeholder so a sparse page buffer can fill
// in behind the scroll.
//
// The @tanstack/react-virtual dependency lives HERE and nowhere else: every
// screen imports CoverGrid, so the library stays swappable behind one file.
export function CoverGrid({
  total,
  renderItem,
  minCardWidth,
  rowHeight,
  gap = 16,
  onRangeChange,
  registerScrollToOffset,
  scrollPaddingTop = 0,
  testId,
  emptyState,
}: {
  total: number
  /** Renders the card at a flat index, or a placeholder when not yet loaded. */
  renderItem: (index: number) => ReactNode
  minCardWidth: number
  rowHeight: number
  gap?: number
  /** Fires with the flat [first, last] index range the viewport covers. */
  onRangeChange?: (first: number, last: number) => void
  /** Hands the caller a scroll function for the A-Z rail's offsets. */
  registerScrollToOffset?: (fn: (offset: number) => void) => void
  /** Px of frozen chrome above the grid (sticky topbar + toolbar) that rail
   * jumps must clear so the target row is not hidden underneath it. */
  scrollPaddingTop?: number
  testId: string
  emptyState?: ReactNode
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [columns, setColumns] = useState(4)

  useLayoutEffect(() => {
    const el = containerRef.current
    if (!el) return
    const measure = () => setColumns(columnsFor(el.clientWidth, minCardWidth, gap))
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [minCardWidth, gap])

  const rows = rowCount(total, columns)
  const virtualizer = useWindowVirtualizer({
    count: rows,
    estimateSize: () => rowHeight,
    overscan: 4,
    scrollMargin: containerRef.current?.offsetTop ?? 0,
  })

  const items = virtualizer.getVirtualItems()

  useEffect(() => {
    if (!onRangeChange || items.length === 0 || total === 0) return
    const [first, last] = itemRange(items[0].index, items[items.length - 1].index, columns, total)
    onRangeChange(first, last)
    // items identity churns per scroll frame; first/last row is the signal.
  }, [items.length > 0 ? items[0].index : -1, items.length > 0 ? items[items.length - 1].index : -1, columns, total]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    registerScrollToOffset?.((offset: number) => {
      const row = rowOfOffset(offset, columns)
      virtualizer.scrollToOffset(jumpScrollOffset(virtualizer.options.scrollMargin, row, rowHeight, scrollPaddingTop))
    })
  }, [registerScrollToOffset, virtualizer, columns, rowHeight, scrollPaddingTop])

  if (total === 0) {
    return (
      <div ref={containerRef} data-testid={testId}>
        {emptyState}
      </div>
    )
  }

  return (
    <div ref={containerRef} data-testid={testId}>
      <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
        {items.map((row) => {
          const [first, last] = itemRange(row.index, row.index, columns, total)
          const cells = []
          for (let i = first; i <= last; i++) cells.push(<div key={i}>{renderItem(i)}</div>)
          return (
            <div
              key={row.key}
              className="absolute left-0 w-full"
              style={{
                top: row.start - virtualizer.options.scrollMargin,
                display: 'grid',
                gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`,
                gap,
              }}
            >
              {cells}
            </div>
          )
        })}
      </div>
    </div>
  )
}
