import { describe, expect, it } from 'vitest'
import { summariseSync } from './collection'
import type { CollectionSyncResult } from '../api/types'

const result = (added: number, removed: number, gaps: number): CollectionSyncResult => ({
  platform: 'atari7800',
  added,
  removed,
  counts: { groups: 60, owned: 60 - gaps, gaps, out: 79, surplus: 0 },
})

describe('summariseSync', () => {
  it('says nothing is monitored when nothing is', () => {
    expect(summariseSync([])).toMatch(/no platform/i)
  })

  it('distinguishes an unchanged list from an updated one', () => {
    expect(summariseSync([result(0, 0, 2)])).toMatch(/unchanged/i)
    expect(summariseSync([result(2, 0, 2)])).toMatch(/2 new gaps/)
  })

  it('sums across platforms', () => {
    const line = summariseSync([result(2, 1, 2), result(3, 0, 4)])
    expect(line).toContain('5 new gaps')
    expect(line).toContain('1 filled')
    expect(line).toContain('6 still wanted')
  })

  it('never claims a download for a removal', () => {
    // A gap leaves the list when it is filled AND when the set stops wanting
    // it — a catalog refresh, a policy change. The wording must cover both.
    expect(summariseSync([result(0, 1, 0)])).toMatch(/filled or no longer wanted/)
  })

  it('keeps singular and plural honest', () => {
    expect(summariseSync([result(1, 0, 1)])).toContain('1 new gap —')
  })
})
