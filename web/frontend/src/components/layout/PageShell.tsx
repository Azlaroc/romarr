import type { ReactNode } from 'react'
import { PageHeader } from './PageHeader'

/**
 * The arr page toolbar: page actions on the left, view controls (Show
 * Advanced, filters) on the right, above a divider.
 *
 * Exported on its own for screens that already manage their own heading, but
 * the normal entry point is PageShell.
 *
 * `sticky` freezes the bar just below the app topbar so navigation (back
 * links) and view controls stay reachable however deep the page scrolls —
 * the arr pattern for long browse screens. The 60px offset must match
 * Topbar's h-[60px]; the negative margins must match main's px-4 sm:px-6 so
 * the opaque bar covers content scrolling past at the column edges.
 */
export function PageToolbar({ actions, tools, sticky = false }: { actions?: ReactNode; tools?: ReactNode; sticky?: boolean }) {
  return (
    <div
      className={
        sticky
          ? 'sticky top-[60px] z-10 -mx-4 mb-6 flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 bg-slate-950/95 px-4 py-2.5 backdrop-blur sm:-mx-6 sm:px-6'
          : 'mb-6 flex flex-wrap items-center justify-between gap-2 border-b border-slate-800 pb-3'
      }
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
  stickyToolbar = false,
  children,
}: {
  title: string
  subtitle?: string
  /** Toolbar left: Add / Refresh / Test — things this page *does*. */
  actions?: ReactNode
  /** Toolbar right: Show Advanced and view toggles — how this page *looks*. */
  tools?: ReactNode
  /** Freeze the toolbar below the topbar while the body scrolls (long browse screens). */
  stickyToolbar?: boolean
  children: ReactNode
}) {
  const hasToolbar = Boolean(actions || tools)
  return (
    <>
      <PageHeader title={title} subtitle={subtitle} className={hasToolbar ? 'mb-3' : 'mb-6'} />
      {hasToolbar && <PageToolbar actions={actions} tools={tools} sticky={stickyToolbar} />}
      {children}
    </>
  )
}
