import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { createMemoryRouter, Link, RouterProvider } from 'react-router-dom'
import { SaveBar, shouldBlockNavigation, UnsavedChangesPrompt } from './SaveBar'

describe('SaveBar', () => {
  it('stays out of the way until there is something to save', () => {
    render(<SaveBar dirty={false} onSave={() => {}} onCancel={() => {}} />)
    expect(screen.queryByTestId('save-bar')).not.toBeInTheDocument()
  })

  it('appears when dirty and reports both actions', () => {
    const onSave = vi.fn()
    const onCancel = vi.fn()
    render(<SaveBar dirty summary="3 platforms changed" onSave={onSave} onCancel={onCancel} />)

    expect(screen.getByTestId('save-bar-summary')).toHaveTextContent('3 platforms changed')
    fireEvent.click(screen.getByTestId('save-bar-save'))
    fireEvent.click(screen.getByTestId('save-bar-cancel'))
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('locks both buttons while a save is in flight so edits cannot be double-submitted', () => {
    render(<SaveBar dirty saving onSave={() => {}} onCancel={() => {}} />)
    expect(screen.getByTestId('save-bar-save')).toBeDisabled()
    expect(screen.getByTestId('save-bar-cancel')).toBeDisabled()
    expect(screen.getByTestId('save-bar-summary')).toHaveTextContent('Saving')
  })
})

function renderGuardedRouter(dirty: boolean) {
  const router = createMemoryRouter(
    [
      {
        path: '/',
        element: (
          <div>
            <UnsavedChangesPrompt dirty={dirty} />
            <Link to="/other">leave</Link>
          </div>
        ),
      },
      { path: '/other', element: <p>other page</p> },
    ],
    { initialEntries: ['/'] },
  )
  render(<RouterProvider router={router} />)
  return router
}

describe('shouldBlockNavigation', () => {
  // The predicate is unit-tested directly because the half that matters most
  // is that it does NOT over-block: a guard that prompts on every click gets
  // muscle-memory-dismissed and stops protecting anything.
  it('blocks only a departure with pending edits', () => {
    expect(shouldBlockNavigation(true, '/settings/metadata', '/library')).toBe(true)
  })

  it('lets a clean page navigate freely', () => {
    expect(shouldBlockNavigation(false, '/settings/metadata', '/library')).toBe(false)
  })

  it('ignores same-path navigation, which is a filter or hash change', () => {
    expect(shouldBlockNavigation(true, '/library', '/library')).toBe(false)
  })
})

describe('UnsavedChangesPrompt', () => {
  // Completing a blocked navigation is exercised in the Playwright journey:
  // react-router's data router builds a Request on navigate, and jsdom's
  // AbortSignal is not the one Node's Request accepts. Rather than shim an
  // AbortController that cannot abort, the engage/cancel half is asserted here
  // and the discard-and-leave half in a real browser.
  it('prompts instead of leaving when edits are pending', async () => {
    renderGuardedRouter(true)
    fireEvent.click(screen.getByText('leave'))

    expect(await screen.findByTestId('confirm-ok')).toBeInTheDocument()
    expect(screen.queryByText('other page')).not.toBeInTheDocument()
  })

  it('cancelling the prompt keeps you on the page with edits intact', async () => {
    renderGuardedRouter(true)
    fireEvent.click(screen.getByText('leave'))
    fireEvent.click(await screen.findByTestId('confirm-cancel'))

    expect(screen.queryByText('other page')).not.toBeInTheDocument()
    expect(screen.getByText('leave')).toBeInTheDocument()
  })

  it('guards tab close only while dirty', () => {
    const add = vi.spyOn(window, 'addEventListener')
    const { unmount } = render(<UnsavedChangesPromptHarness dirty={false} />)
    expect(add.mock.calls.some(([type]) => type === 'beforeunload')).toBe(false)
    unmount()

    render(<UnsavedChangesPromptHarness dirty />)
    expect(add.mock.calls.some(([type]) => type === 'beforeunload')).toBe(true)
  })
})

// The blocker needs a data router; this harness supplies one so the
// beforeunload assertion above can focus on the tab-close half.
function UnsavedChangesPromptHarness({ dirty }: { dirty: boolean }) {
  const router = createMemoryRouter([{ path: '/', element: <UnsavedChangesPrompt dirty={dirty} /> }], {
    initialEntries: ['/'],
  })
  return <RouterProvider router={router} />
}
