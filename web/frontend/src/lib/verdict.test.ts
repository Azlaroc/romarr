import { describe, expect, it } from 'vitest'
import { verdictChip } from './verdict'

describe('verdictChip', () => {
  it('renders the empty verdict as calm not-measured, never as a fault', () => {
    expect(verdictChip('')).toEqual({ label: 'Not measured yet', color: 'slate' })
    expect(verdictChip(undefined)).toEqual({ label: 'Not measured yet', color: 'slate' })
  })
  it('keeps mismatch and unknown visually distinct', () => {
    expect(verdictChip('mismatch').color).toBe('red')
    expect(verdictChip('unknown').color).not.toBe('red')
    expect(verdictChip('verified').color).toBe('emerald')
  })
})
