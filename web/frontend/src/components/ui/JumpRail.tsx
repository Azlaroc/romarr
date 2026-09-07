import type { LibraryLetter } from '../../api/types'

// The A-Z jump rail: one stop per letter the server reports, offsets straight
// off /api/library/letters. Letters with no rows are simply absent (the
// zero-count-hidden law) — a rail of 26 dead buttons tells you nothing.
export function JumpRail({
  letters,
  onJump,
  activeLetter,
  testId = 'jump-rail',
}: {
  letters: LibraryLetter[]
  onJump: (offset: number) => void
  activeLetter?: string
  testId?: string
}) {
  if (letters.length <= 1) return null
  return (
    <nav
      className="sticky top-20 flex max-h-[70vh] flex-col items-center gap-0.5 overflow-y-auto px-1"
      aria-label="Jump to letter"
      data-testid={testId}
    >
      {letters.map((l) => (
        <button
          key={l.letter}
          type="button"
          title={`${l.letter} — ${l.count}`}
          onClick={() => onJump(l.offset)}
          data-testid={`${testId}-${l.letter === '#' ? 'num' : l.letter}`}
          className={`rounded px-1.5 py-0.5 text-xs font-medium leading-4 ${
            activeLetter === l.letter
              ? 'bg-accent-600 text-white'
              : 'text-slate-500 hover:bg-slate-800 hover:text-slate-200'
          }`}
        >
          {l.letter}
        </button>
      ))}
    </nav>
  )
}
