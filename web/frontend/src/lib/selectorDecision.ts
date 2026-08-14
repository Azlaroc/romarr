/**
 * The scheduler flattens selector decisions into the activity `detail` string:
 * `[mode] action: reason (grabs=N, rejected=M; legacy pick: X)`
 * (internal/scheduler/scheduler.go). Parse tolerantly — callers must render
 * the raw detail on a miss, never hide it.
 */
const SELECTOR_RE = /^\[(\w+)\]\s+(\w+):\s*(.*)$/

export interface SelectorDecision {
  mode: string // off | shadow | enforce
  action: string // grab | grab_set | skip
  rest: string // reason + counters, pre-formatted
}

export function parseSelectorDecision(detail: string): SelectorDecision | null {
  const m = SELECTOR_RE.exec(detail)
  if (!m) return null
  return { mode: m[1], action: m[2], rest: m[3] }
}
