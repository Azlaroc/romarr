import { useEffect, type ReactNode } from 'react'
import { useBlocker } from 'react-router-dom'
import { Button } from './Button'
import { ConfirmDialog } from './ConfirmDialog'
import { Spinner } from './Spinner'

/**
 * Sticky save bar for screens that batch their edits.
 *
 * Most settings screens in this app save per control (toggle on change, input
 * on blur). That works for independent switches; it does not work for a table
 * of related numbers where you want to review several edits before committing
 * them. Screens built on this bar hold their pending edits locally and write
 * once, which is also the arr idiom.
 *
 * Actions are not edits: buttons that trigger work on the server (Refresh,
 * Upload, Reset) stay immediate and do not participate in dirty state.
 */
export function SaveBar({
  dirty,
  saving = false,
  summary,
  saveLabel = 'Save Changes',
  onSave,
  onCancel,
}: {
  dirty: boolean
  saving?: boolean
  /** Short description of what is pending, e.g. "3 platforms changed". */
  summary?: ReactNode
  saveLabel?: string
  onSave: () => void
  onCancel: () => void
}) {
  if (!dirty) return null
  return (
    <div
      className="sticky bottom-0 z-30 mt-6 flex flex-wrap items-center justify-between gap-3 rounded-t border border-b-0 border-slate-700 bg-slate-850 px-4 py-3 shadow-[0_-4px_12px_rgba(0,0,0,0.35)]"
      data-testid="save-bar"
    >
      <div className="text-xs text-slate-400" data-testid="save-bar-summary">
        {saving ? <Spinner label="Saving…" /> : (summary ?? 'You have unsaved changes.')}
      </div>
      <div className="flex items-center gap-2">
        <Button variant="secondary" size="sm" onClick={onCancel} disabled={saving} data-testid="save-bar-cancel">
          Cancel
        </Button>
        <Button size="sm" onClick={onSave} disabled={saving} data-testid="save-bar-save">
          {saveLabel}
        </Button>
      </div>
    </div>
  )
}

/**
 * Blocks in-app navigation and tab close while edits are pending.
 *
 * Kept separate from SaveBar so the bar stays presentational: this half needs a
 * data router, the bar does not.
 */
export function shouldBlockNavigation(dirty: boolean, currentPath: string, nextPath: string): boolean {
  // A same-path navigation is a search/hash change, not a departure — blocking
  // those would fire the prompt on a filter click.
  return dirty && currentPath !== nextPath
}

export function UnsavedChangesPrompt({ dirty }: { dirty: boolean }) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) =>
    shouldBlockNavigation(dirty, currentLocation.pathname, nextLocation.pathname),
  )

  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])

  return (
    <ConfirmDialog
      open={blocker.state === 'blocked'}
      title="Discard unsaved changes?"
      message="This page has edits that have not been saved. Leaving now discards them."
      confirmLabel="Discard"
      danger
      onConfirm={() => blocker.proceed?.()}
      onCancel={() => blocker.reset?.()}
    />
  )
}
