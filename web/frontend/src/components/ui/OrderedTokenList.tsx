import { useState } from 'react'
import { ArrowDown, ArrowUp, Plus, X } from 'lucide-react'
import { inputCls } from './Input'
import { Button } from './Button'

interface Props {
  /** Ordered tokens, best first. */
  value: string[]
  onChange: (next: string[]) => void
  /** Fixed vocabulary — unselected tokens render as add-chips below the list. */
  available?: readonly string[]
  /** Free-text entry (e.g. source ranking). */
  allowCustom?: boolean
  disabled?: boolean
  testid: string
  emptyLabel?: string
}

/**
 * Reorderable token list with up/down/remove buttons (deliberately no
 * drag-and-drop — no extra deps, CSP-simple, and trivially testable).
 * The list container stays mounted when empty.
 */
export function OrderedTokenList({ value, onChange, available, allowCustom, disabled, testid, emptyLabel = 'None — using defaults' }: Props) {
  const [custom, setCustom] = useState('')

  const move = (i: number, delta: number) => {
    const next = [...value]
    const [tok] = next.splice(i, 1)
    next.splice(i + delta, 0, tok)
    onChange(next)
  }
  const remove = (i: number) => onChange(value.filter((_, idx) => idx !== i))
  const add = (tok: string) => {
    const t = tok.trim()
    if (t && !value.includes(t)) onChange([...value, t])
  }
  const addCustom = () => {
    add(custom)
    setCustom('')
  }

  const unselected = (available ?? []).filter((t) => !value.includes(t))

  return (
    <div>
      <div className="space-y-1" data-testid={testid}>
        {value.map((tok, i) => (
          <div key={tok} className="flex items-center gap-2 rounded-lg border border-slate-800 bg-slate-800/50 px-3 py-1.5" data-testid={`${testid}-item-${i}`}>
            <span className="w-5 text-right text-xs text-slate-500">{i + 1}.</span>
            <span className="flex-1 truncate text-sm text-slate-200">{tok}</span>
            <button
              type="button"
              onClick={() => move(i, -1)}
              disabled={disabled || i === 0}
              className="text-slate-500 hover:text-slate-200 disabled:cursor-not-allowed disabled:opacity-30"
              aria-label={`Move ${tok} up`}
              data-testid={`${testid}-up-${i}`}
            >
              <ArrowUp className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              onClick={() => move(i, 1)}
              disabled={disabled || i === value.length - 1}
              className="text-slate-500 hover:text-slate-200 disabled:cursor-not-allowed disabled:opacity-30"
              aria-label={`Move ${tok} down`}
              data-testid={`${testid}-down-${i}`}
            >
              <ArrowDown className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              onClick={() => remove(i)}
              disabled={disabled}
              className="text-slate-500 hover:text-red-400 disabled:cursor-not-allowed disabled:opacity-30"
              aria-label={`Remove ${tok}`}
              data-testid={`${testid}-remove-${i}`}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
        {value.length === 0 && <div className="rounded-lg border border-dashed border-slate-800 px-3 py-2 text-xs text-slate-600">{emptyLabel}</div>}
      </div>

      {unselected.length > 0 && !disabled && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {unselected.map((tok) => (
            <button
              type="button"
              key={tok}
              onClick={() => add(tok)}
              className="rounded-full border border-slate-700 bg-slate-800 px-2.5 py-0.5 text-xs text-slate-400 transition-colors hover:border-accent-500 hover:text-accent-300"
              data-testid={`${testid}-add-${tok}`}
            >
              + {tok}
            </button>
          ))}
        </div>
      )}

      {allowCustom && !disabled && (
        <div className="mt-2 flex gap-2">
          <input
            value={custom}
            onChange={(e) => setCustom(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                addCustom()
              }
            }}
            placeholder="Add entry"
            className={`${inputCls} flex-1`}
            data-testid={`${testid}-input`}
          />
          <Button type="button" variant="secondary" size="sm" onClick={addCustom} disabled={!custom.trim()} data-testid={`${testid}-add`}>
            <Plus className="h-3.5 w-3.5" /> Add
          </Button>
        </div>
      )}
    </div>
  )
}
