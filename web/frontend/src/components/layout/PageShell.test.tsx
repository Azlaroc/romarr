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
    const toolbar = screen.getByTestId('page-toolbar')
    expect(toolbar).toBeInTheDocument()
    expect(toolbar.className).not.toContain('sticky')
    expect(screen.getByTestId('page-toolbar-actions')).toHaveTextContent('Refresh')
    expect(screen.getByTestId('page-toolbar-tools')).toHaveTextContent('Show Advanced')
  })

  it('freezes the toolbar below the topbar when a browse screen asks for it', () => {
    render(
      <PageShell title="Library" stickyToolbar actions={<button>Back</button>}>
        <p>body</p>
      </PageShell>,
    )
    const toolbar = screen.getByTestId('page-toolbar')
    expect(toolbar.className).toContain('sticky')
    // The stop must clear the 60px topbar, and the bar must be opaque so the
    // grid does not scroll through it.
    expect(toolbar.className).toContain('top-[60px]')
    expect(toolbar.className).toContain('bg-slate-950')
    expect(screen.getByTestId('page-toolbar-actions')).toHaveTextContent('Back')
  })
})
