import type { CollectionSyncResult } from '../api/types'

/**
 * What a gap-list update actually did, in one line.
 *
 * `removed` covers two different endings — a gap that was filled and a gap the
 * set no longer wants (a catalog refresh, a policy change) — and the wording
 * says so rather than claiming a download happened.
 */
export function summariseSync(results: CollectionSyncResult[]): string {
  if (!results.length) return 'No platform is in collection mode yet.'
  const added = results.reduce((n, r) => n + r.added, 0)
  const removed = results.reduce((n, r) => n + r.removed, 0)
  const gaps = results.reduce((n, r) => n + r.counts.gaps, 0)
  if (!added && !removed) return `Gap list unchanged — ${gaps} still wanted.`
  const parts: string[] = []
  if (added) parts.push(`${added} new ${added === 1 ? 'gap' : 'gaps'}`)
  if (removed) parts.push(`${removed} filled or no longer wanted`)
  return `Gap list updated: ${parts.join(', ')} — ${gaps} still wanted.`
}
