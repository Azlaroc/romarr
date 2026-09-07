import type { BadgeColor } from '../components/ui/Badge'

// The catalog-verdict chip vocabulary, in one place so every level renders
// the same words in the same colors. The empty verdict is a first-class
// state: ~90% of a freshly-scanned library was never measured (the zero-I/O
// scan is a design choice, not a failure), so "not measured yet" must read
// as calm slate, visually and verbally distinct from a red mismatch.
export function verdictChip(verdict: string | undefined | null): { label: string; color: BadgeColor } {
  switch (verdict) {
    case 'verified':
      return { label: 'Verified', color: 'emerald' }
    case 'mismatch':
      return { label: 'Mismatch', color: 'red' }
    case 'unknown':
      return { label: 'Uncatalogued', color: 'purple' }
    default:
      return { label: 'Not measured yet', color: 'slate' }
  }
}
