import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FormGroup } from './FormGroup'
import { FormRow } from './FormRow'

describe('FormGroup', () => {
  it('renders as a fieldset with a legend, description and its rows', () => {
    const { container } = render(
      <FormGroup title="Cadence" description="How often catalogs refresh" data-testid="dat-cadence">
        <FormRow label="Interval" hint="days">
          <input aria-label="interval" />
        </FormRow>
      </FormGroup>,
    )
    expect(container.querySelector('fieldset')).toBeInTheDocument()
    expect(container.querySelector('legend')).toHaveTextContent('Cadence')
    expect(screen.getByText('How often catalogs refresh')).toBeInTheDocument()
    expect(screen.getByText('Interval')).toBeInTheDocument()
    expect(screen.getByLabelText('interval')).toBeInTheDocument()
    expect(screen.getByTestId('dat-cadence')).toBeInTheDocument()
  })

  it('renders a heading-row action when one is supplied', () => {
    render(
      <FormGroup title="Authorities" action={<button>Refresh now</button>}>
        <p>rows</p>
      </FormGroup>,
    )
    expect(screen.getByRole('button', { name: 'Refresh now' })).toBeInTheDocument()
  })
})
