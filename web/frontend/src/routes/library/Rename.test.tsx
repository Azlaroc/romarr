import { describe, expect, it } from 'vitest'
import { sourceBadge } from './Rename'

describe('sourceBadge', () => {
  it('marks local-snapshot proposals neutral', () => {
    expect(sourceBadge('dat')).toEqual({ color: 'blue', label: 'DAT' })
  })
  it('marks online-fallback proposals as warning', () => {
    expect(sourceBadge('playmatch')).toEqual({ color: 'yellow', label: 'Playmatch' })
  })
  it('renders nothing when no authority decided', () => {
    expect(sourceBadge(undefined)).toBeNull()
    expect(sourceBadge('')).toBeNull()
  })
})
