import { fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { DataTable } from './DataTable'
import type { DataTableColumn } from './DataTable'

interface Row {
  id: string
  name: string
  n: number
}

const columns: DataTableColumn<Row>[] = [
  { accessorKey: 'name', header: 'Name', size: 200 },
  { accessorKey: 'n', header: 'Number', size: 100 },
]

const makeRows = (count: number): Row[] =>
  Array.from({ length: count }, (_, i) => ({ id: `r${i}`, name: `row ${i}`, n: i }))

// jsdom has no layout: give the scroll area a size so the virtualizer sees it.
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(480)
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(800)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    width: 800, height: 480, top: 0, left: 0, right: 800, bottom: 480, x: 0, y: 0, toJSON: () => ({}),
  })
})
afterEach(() => vi.restoreAllMocks())

const props = { columns, getRowId: (r: Row) => r.id, label: 'Test rows' }

test('renders only a window of 5,000 rows', () => {
  render(<DataTable {...props} data={makeRows(5000)} />)
  const body = screen.getByRole('table', { name: 'Test rows' })
  const rendered = within(body).getAllByRole('row').length
  // 480px / 36px = 14 visible rows, plus overscan, plus the header row.
  expect(rendered).toBeGreaterThan(5)
  expect(rendered).toBeLessThan(40)
  expect(body.getAttribute('aria-rowcount')).toBe('5001')
  expect(screen.getByText('row 0')).toBeTruthy()
  expect(screen.queryByText('row 4999')).toBeNull()
})

test('shows loading, error and empty states instead of rows', () => {
  const { rerender } = render(<DataTable {...props} data={[]} isLoading />)
  expect(screen.getByRole('status').textContent).toContain('Loading')
  rerender(<DataTable {...props} data={[]} error={new Error('x')} />)
  expect(screen.getByRole('alert')).toBeTruthy()
  rerender(<DataTable {...props} data={[]} emptyMessage="No calls in this range." />)
  expect(screen.getByText('No calls in this range.')).toBeTruthy()
})

test('sorts the loaded rows from the header', () => {
  render(<DataTable {...props} data={makeRows(5)} />)
  const header = screen.getByRole('columnheader', { name: /Number/ })
  // A numeric column sorts descending first.
  fireEvent.click(within(header).getByRole('button'))
  expect(header.getAttribute('aria-sort')).toBe('descending')
  const rows = screen.getAllByRole('row').slice(1)
  expect(within(rows[0]).getByText('row 4')).toBeTruthy()
})

test('column visibility toggle hides a column', () => {
  render(<DataTable {...props} data={makeRows(3)} />)
  fireEvent.click(screen.getByLabelText('Number'))
  expect(screen.queryByRole('columnheader', { name: /Number/ })).toBeNull()
  expect(screen.getByRole('columnheader', { name: /Name/ })).toBeTruthy()
})

test('row click and Enter call onRowClick; end of list asks for more', () => {
  const onRowClick = vi.fn()
  const onEndReached = vi.fn()
  render(<DataTable {...props} data={makeRows(3)} onRowClick={onRowClick} onEndReached={onEndReached} hasNextPage />)
  const row = screen.getAllByRole('row')[1]
  fireEvent.click(row)
  fireEvent.keyDown(row, { key: 'Enter' })
  expect(onRowClick).toHaveBeenCalledTimes(2)
  expect(onEndReached).toHaveBeenCalled()
})

test('end of list does not ask for more when there is no next page', () => {
  const onEndReached = vi.fn()
  render(<DataTable {...props} data={makeRows(3)} onEndReached={onEndReached} hasNextPage={false} />)
  expect(onEndReached).not.toHaveBeenCalled()
})

test('the column menu is a native popover with one checkbox per column', () => {
  render(<DataTable {...props} data={makeRows(3)} />)
  const button = screen.getByRole('button', { name: 'Columns' })
  const panel = document.querySelector<HTMLElement>('.column-list')!
  // The browser owns open state, light dismiss, Escape and focus return; jsdom has no popover engine.
  expect(panel.getAttribute('popover')).toBe('auto')
  expect(button.getAttribute('popovertarget')).toBe(panel.id)
  expect(within(panel).getAllByRole('checkbox', { hidden: true }).length).toBe(columns.length)
  fireEvent.click(within(panel).getAllByRole('checkbox', { hidden: true })[1])
  expect(screen.queryByRole('columnheader', { name: /Number/ })).toBeNull()
})
