import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Database } from 'lucide-react'
import { DataTable, type Column } from './DataTable'

interface Row {
  slug: string
  known: number
}

const rows: Row[] = [
  { slug: 'nes', known: 14132 },
  { slug: 'atari2600', known: 905 },
  { slug: 'gb', known: 2254 },
]

const columns: Column<Row>[] = [
  { key: 'slug', header: 'Platform', render: (r) => r.slug, sortValue: (r) => r.slug },
  { key: 'known', header: 'Known', render: (r) => r.known, sortValue: (r) => r.known, align: 'right' },
  { key: 'actions', header: '', render: () => <button>Reset</button>, align: 'right' },
]

const renderTable = (props: Partial<Parameters<typeof DataTable<Row>>[0]> = {}) =>
  render(
    <DataTable<Row>
      columns={columns}
      rows={rows}
      rowKey={(r) => r.slug}
      testId="qd-table"
      empty={{ icon: Database, title: 'No definitions' }}
      {...props}
    />,
  )

describe('DataTable', () => {
  it('renders a real table with one row per record', () => {
    renderTable()
    expect(screen.getByTestId('qd-table').tagName).toBe('TABLE')
    expect(screen.getAllByRole('row')).toHaveLength(rows.length + 1) // + header
    expect(screen.getByTestId('row-nes')).toHaveTextContent('14132')
  })

  it('shows skeletons while loading and never the empty state', () => {
    renderTable({ loading: true, rows: [] })
    expect(screen.getByTestId('qd-table-loading')).toBeInTheDocument()
    expect(screen.queryByTestId('qd-table-empty')).not.toBeInTheDocument()
    expect(screen.queryByTestId('qd-table')).not.toBeInTheDocument()
  })

  it('falls back to the empty state once loading is done with no rows', () => {
    renderTable({ rows: [] })
    expect(screen.getByTestId('qd-table-empty')).toHaveTextContent('No definitions')
    expect(screen.queryByTestId('qd-table')).not.toBeInTheDocument()
  })

  it('sorts on a sortable header and reverses on a second click', () => {
    renderTable({ initialSort: { key: 'slug' } })
    const order = () => screen.getAllByRole('row').slice(1).map((r) => r.getAttribute('data-testid'))
    expect(order()).toEqual(['row-atari2600', 'row-gb', 'row-nes'])

    fireEvent.click(screen.getByTestId('sort-known'))
    expect(order()).toEqual(['row-atari2600', 'row-gb', 'row-nes']) // numeric ascending

    fireEvent.click(screen.getByTestId('sort-known'))
    expect(order()).toEqual(['row-nes', 'row-gb', 'row-atari2600'])
  })

  it('offers no sort control for columns that did not opt in', () => {
    renderTable()
    expect(screen.queryByTestId('sort-actions')).not.toBeInTheDocument()
  })

  it('renders pagination only when the caller controls it', () => {
    const onPageChange = vi.fn()
    const { rerender } = renderTable()
    expect(screen.queryByLabelText('Pagination')).not.toBeInTheDocument()

    rerender(
      <DataTable<Row>
        columns={columns}
        rows={rows}
        rowKey={(r) => r.slug}
        testId="qd-table"
        page={1}
        totalPages={3}
        onPageChange={onPageChange}
      />,
    )
    expect(screen.getByLabelText('Pagination')).toBeInTheDocument()
  })
})
