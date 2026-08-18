# RomArr web UI

React + TypeScript + Tailwind SPA, built by Vite into `web/dist`, which the Go
binary embeds and serves under a strict CSP.

```bash
npm ci
npm run dev        # vite dev server on :5173, proxying /api to the Go app on :5001
npm run typecheck  # tsc --noEmit
npm run test       # vitest component tests
npm run build      # typecheck + production bundle into ../dist
```

## Start here when building a screen

**New screens are composed from the shared page template — do not hand-roll the
chrome.** The pieces and what each is for:

| Import | Use it for |
|---|---|
| `components/layout/PageShell` | The page frame: heading, then a toolbar with page actions on the left and view controls (Show Advanced) on the right. |
| `components/ui/FormGroup` | A fieldset section of a form. Wraps `FormRow` children (label, control, hint, advanced flag). |
| `components/ui/DataTable` | Tabular data. Sortable headers, right-aligned row actions, with loading / empty / paged states already wired to `Skeleton`, `EmptyState` and `Pagination`. |
| `components/ui/SaveBar` + `UnsavedChangesPrompt` | Screens that batch edits: a sticky bar that appears when dirty, plus the navigation and tab-close guard. |
| `components/ui/InfoPopover` | The `(?)` beside a field label, for explaining a setting without putting a paragraph in the form. |

This template exists because the arr structural chrome was originally meant to
be re-created by each screen as it was built, and after six screens none of it
had been. "Every screen remembers to carry a piece" does not hold; "build the
shell once, screens import it" does. If you find yourself writing a `<table>`,
a page heading, or a save button by hand, reach for the component instead — and
if it doesn't fit, extend the shared one rather than forking it locally.

Two settings idioms coexist deliberately. Older screens save **per control**
(toggle on change, input on blur), which suits independent switches. Screens
built on `SaveBar` hold pending edits and write **once**, which suits related
values you want to review together. Buttons that trigger server-side work
(Refresh, Upload, Reset) are actions, not edits — they stay immediate either
way.

## Conventions

- Every interactive element carries a `data-testid`; the Playwright suite in
  `e2e/` navigates by them, using a per-screen prefix (`md-*`, `qd-*`, `idx-*`).
- Advanced fields take the `advanced` prop for the arr orange accent, gated by
  `useShowAdvanced` (in `lib/`, shared across pages via localStorage).
- API access goes through `api/client.ts`; queries and mutations are declared in
  `api/queries.ts` with keys centralized in its `keys` object. Admin-gated
  screens render `<AdminNotice />` when `isForbidden(error)`.
