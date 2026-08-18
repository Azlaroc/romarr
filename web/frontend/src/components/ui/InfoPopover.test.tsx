import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { InfoPopover } from './InfoPopover'

describe('InfoPopover', () => {
  it('opens on click and closes on Escape', () => {
    render(<InfoPopover label="Minimum size">Zero means unlimited.</InfoPopover>)
    expect(screen.queryByTestId('info-popover-panel')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('info-popover-trigger'))
    expect(screen.getByTestId('info-popover-panel')).toHaveTextContent('Zero means unlimited.')

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByTestId('info-popover-panel')).not.toBeInTheDocument()
  })

  it('closes when the click lands outside it', () => {
    render(
      <div>
        <InfoPopover>help text</InfoPopover>
        <button>elsewhere</button>
      </div>,
    )
    fireEvent.click(screen.getByTestId('info-popover-trigger'))
    expect(screen.getByTestId('info-popover-panel')).toBeInTheDocument()

    fireEvent.mouseDown(screen.getByRole('button', { name: 'elsewhere' }))
    expect(screen.queryByTestId('info-popover-panel')).not.toBeInTheDocument()
  })
})
