import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PageShell } from './PageShell'

describe('PageShell', () => {
  it('renders the heading and body without a toolbar when no controls are given', () => {
    render(
      <PageShell title="Settings" subtitle="Metadata">
        <p>body</p>
      </PageShell>,
    )
    expect(screen.getByTestId('page-title')).toHaveTextContent('Settings')
    expect(screen.getByText('Metadata')).toBeInTheDocument()
    expect(screen.getByText('body')).toBeInTheDocument()
    expect(screen.queryByTestId('page-toolbar')).not.toBeInTheDocument()
  })

  it('separates page actions from view tools so their placement is not a per-screen decision', () => {
    render(
      <PageShell title="Settings" actions={<button>Refresh</button>} tools={<button>Show Advanced</button>}>
        <p>body</p>
      </PageShell>,
    )
    expect(screen.getByTestId('page-toolbar')).toBeInTheDocument()
    expect(screen.getByTestId('page-toolbar-actions')).toHaveTextContent('Refresh')
    expect(screen.getByTestId('page-toolbar-tools')).toHaveTextContent('Show Advanced')
  })
})
