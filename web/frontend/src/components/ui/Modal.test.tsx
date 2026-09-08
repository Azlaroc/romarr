import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Modal } from './Modal'

describe('Modal', () => {
  it('renders nothing when closed', () => {
    render(
      <Modal open={false} onClose={() => {}} title="Hidden">
        <p>body</p>
      </Modal>,
    )
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('caps its height and scrolls the body, keeping the header pinned', () => {
    render(
      <Modal open onClose={() => {}} title="Search: something">
        <p>body</p>
      </Modal>,
    )
    const dialog = screen.getByRole('dialog')
    // A dialog taller than the viewport must scroll INTERNALLY: a centered
    // flex child that grows past the viewport overflows into negative
    // scroll space nothing can reach, cutting off the top of long lists.
    expect(dialog.className).toContain('max-h-')
    expect(dialog.className).toContain('flex-col')
    const body = dialog.lastElementChild as HTMLElement
    expect(body.className).toContain('overflow-y-auto')
    expect(body.className).toContain('min-h-0')
    const header = dialog.firstElementChild as HTMLElement
    expect(header.className).toContain('shrink-0')
    expect(screen.getByText('Search: something')).toBeInTheDocument()
  })
})
