// Pure grid arithmetic for CoverGrid, extracted so the responsive column
// count and the A-Z rail's offset→row mapping are unit-testable without any
// DOM. The invariant that matters: every reader of an offset (the letters
// endpoint, the sparse page buffer, scrollToOffset) must agree on the same
// column count, or a rail jump lands one row off.

/** Columns that fit a container: at least min 2, each card at least
 * minCardWidth wide, gap between them. */
export function columnsFor(containerWidth: number, minCardWidth: number, gap: number): number {
  if (containerWidth <= 0) return 2
  return Math.max(2, Math.floor((containerWidth + gap) / (minCardWidth + gap)))
}

/** Total virtual rows for a count at a column width. */
export function rowCount(total: number, columns: number): number {
  if (total <= 0 || columns <= 0) return 0
  return Math.ceil(total / columns)
}

/** The row a flat item offset lives on. */
export function rowOfOffset(offset: number, columns: number): number {
  if (columns <= 0) return 0
  return Math.floor(Math.max(0, offset) / columns)
}

/** The flat item index range [first, last] covered by a row range. */
export function itemRange(firstRow: number, lastRow: number, columns: number, total: number): [number, number] {
  const first = Math.max(0, firstRow * columns)
  const last = Math.min(total - 1, (lastRow + 1) * columns - 1)
  return [first, Math.max(first, last)]
}

/** Server pages that cover a flat item range (1-based page numbers). */
export function pagesForRange(first: number, last: number, pageSize: number): number[] {
  if (pageSize <= 0 || last < first) return []
  const from = Math.floor(first / pageSize) + 1
  const to = Math.floor(last / pageSize) + 1
  const out: number[] = []
  for (let p = from; p <= to; p++) out.push(p)
  return out
}
