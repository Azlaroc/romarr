import { describe, expect, it } from 'vitest'
import { columnsFor, itemRange, jumpScrollOffset, pagesForRange, rowCount, rowOfOffset } from './coverGridMath'

describe('coverGridMath', () => {
  it('columns respond to width with a floor of 2', () => {
    expect(columnsFor(0, 160, 16)).toBe(2)
    expect(columnsFor(200, 160, 16)).toBe(2)
    expect(columnsFor(720, 160, 16)).toBe(4) // (720+16)/(160+16) = 4.18
    expect(columnsFor(1280, 160, 16)).toBe(7)
  })

  it('row arithmetic round-trips offsets', () => {
    expect(rowCount(0, 5)).toBe(0)
    expect(rowCount(17177, 6)).toBe(2863)
    expect(rowOfOffset(0, 6)).toBe(0)
    expect(rowOfOffset(679, 6)).toBe(113) // the '#' bucket boundary on prod
    // The invariant the A-Z rail rides on: scrolling to rowOfOffset(o) shows
    // a row whose item range contains o.
    for (const [offset, cols] of [
      [679, 6],
      [17176, 6],
      [1, 2],
      [42, 7],
    ] as const) {
      const row = rowOfOffset(offset, cols)
      const [first, last] = itemRange(row, row, cols, 20000)
      expect(offset).toBeGreaterThanOrEqual(first)
      expect(offset).toBeLessThanOrEqual(last)
    }
  })

  it('rail jumps clear the frozen chrome instead of landing under it', () => {
    // Row 113 at prod geometry, 124px of topbar+toolbar above the grid.
    expect(jumpScrollOffset(200, 113, 318, 124)).toBe(200 + 113 * 318 - 124)
    // Without frozen chrome this is exactly the row's start (old behavior).
    expect(jumpScrollOffset(200, 113, 318, 0)).toBe(200 + 113 * 318)
    // Rows near the top never ask the window for a negative scroll.
    expect(jumpScrollOffset(60, 0, 318, 124)).toBe(0)
  })

  it('item ranges clamp to the collection', () => {
    expect(itemRange(0, 0, 4, 3)).toEqual([0, 2])
    expect(itemRange(2, 3, 5, 17)).toEqual([10, 16])
  })

  it('pages cover a range at the buffer page size', () => {
    expect(pagesForRange(0, 99, 100)).toEqual([1])
    expect(pagesForRange(95, 105, 100)).toEqual([1, 2])
    expect(pagesForRange(250, 260, 100)).toEqual([3])
    expect(pagesForRange(10, 5, 100)).toEqual([])
  })
})
