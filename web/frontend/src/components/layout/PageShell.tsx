import type { ReactNode } from 'react'
import { PageHeader } from './PageHeader'

/**
 * The arr page toolbar: page actions on the left, view controls (Show
 * Advanced, filters) on the right, above a divider.
 *
 * Exported on its own for screens that already manage their own heading, but
 * the normal entry point is PageShell.
 */
export function PageToolbar({ actions, tools }: { actions?: ReactNode; tools?: ReactNode }) {
  return (
    <div
      className="mb-6 flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 pb-3"
      data-testid="page-toolbar"
    >
      <div className="flex flex-wrap items-center gap-2" data-testid="page-toolbar-actions">
        {actions}
      </div>
      <div className="flex flex-wrap items-center gap-2" data-testid="page-toolbar-tools">
        {tools}
      </div>
    </div>
  )
}

/**
 * Page frame every new screen is built from: heading, then the toolbar, then
 * the page body.
 *
 * This exists because the arr structural chrome was previously meant to be
 * re-created per screen and consistently was not — Show Advanced ended up in a
 * different place on each page that had it at all. Importing one shell is what
 * makes the placement stop being a per-screen decision.
 */
export function PageShell({
  title,
  subtitle,
  actions,
  tools,
  children,
}: {
  title: string
  subtitle?: string
  /** Toolbar left: Add / Refresh / Test — things this page *does*. */
  actions?: ReactNode
  /** Toolbar right: Show Advanced and view toggles — how this page *looks*. */
  tools?: ReactNode
  children: ReactNode
}) {
  const hasToolbar = Boolean(actions || tools)
  return (
    <>
      <PageHeader title={title} subtitle={subtitle} className={hasToolbar ? 'mb-3' : 'mb-6'} />
      {hasToolbar && <PageToolbar actions={actions} tools={tools} />}
      {children}
    </>
  )
}
