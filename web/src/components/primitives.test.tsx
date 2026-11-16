import { fireEvent, render, screen } from '@testing-library/react'
import axe from 'axe-core'
import { expect, test, vi } from 'vitest'
import { ContentNotStored } from './ContentNotStored'
import { BooleanFilter, FilterMenu, NumberFilter, TextListFilter } from './FilterMenu'
import { InfoNote } from './InfoNote'
import { TimeRangePicker } from './TimeRangePicker'

test('time range picker reports presets with all time keys, default as undefined', () => {
  const onChange = vi.fn()
  render(<TimeRangePicker value={{ range: '7d' }} onChange={onChange} />)
  expect(screen.getByRole('button', { name: '7d' }).getAttribute('aria-pressed')).toBe('true')
  fireEvent.click(screen.getByRole('button', { name: '1h' }))
  expect(onChange.mock.calls[0][0]).toStrictEqual({ range: '1h', from: undefined, to: undefined })
  fireEvent.click(screen.getByRole('button', { name: '24h' }))
  expect(onChange.mock.calls[1][0]).toStrictEqual({ range: undefined, from: undefined, to: undefined })
})

test('time range picker output spread over the current search clears the other time keys', () => {
  const onChange = vi.fn()
  const current = { model: ['a'], from: '2026-01-01T00:00:00Z', to: '2026-01-02T00:00:00Z' }
  render(<TimeRangePicker value={current} onChange={onChange} />)
  fireEvent.click(screen.getByRole('button', { name: '1h' }))
  expect({ ...current, ...onChange.mock.calls[0][0] }).toStrictEqual({
    model: ['a'], range: '1h', from: undefined, to: undefined,
  })
  const preset = { model: ['a'], range: '7d' as const }
  fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-01-01T10:00' } })
  const merged = { ...preset, ...onChange.mock.calls[1][0] }
  expect(merged.range).toBeUndefined()
  expect(merged.model).toEqual(['a'])
})

test('time range picker reports an absolute bound and clears the preset', () => {
  const onChange = vi.fn()
  render(<TimeRangePicker value={{ range: '7d' }} onChange={onChange} />)
  fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-01-01T10:00' } })
  const arg = onChange.mock.calls[0][0] as { range?: string; from: string; to?: string }
  expect(Object.keys(arg).sort()).toEqual(['from', 'range', 'to'])
  expect(arg.range).toBeUndefined()
  expect(Date.parse(arg.from)).toBe(new Date('2026-01-01T10:00').getTime())
  fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-01-02T10:00' } })
  const toArg = onChange.mock.calls[1][0] as { range?: string; to: string }
  expect(Object.keys(toArg).sort()).toEqual(['from', 'range', 'to'])
  expect(toArg.range).toBeUndefined()
  expect(Date.parse(toArg.to)).toBe(new Date('2026-01-02T10:00').getTime())
})

test('filter menu fields add and remove values', () => {
  const onList = vi.fn()
  const onBool = vi.fn()
  const onNum = vi.fn()
  render(
    <FilterMenu activeCount={2} onClear={() => {}}>
      <TextListFilter label="Model" values={['a']} onChange={onList} />
      <BooleanFilter label="Streamed" value={undefined} onChange={onBool} />
      <NumberFilter label="Latency at least" value={undefined} onChange={onNum} />
    </FilterMenu>,
  )
  expect(screen.getByRole('button', { name: 'Filters (2)' })).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: 'Filters (2)' }))
  fireEvent.change(screen.getByLabelText('Model'), { target: { value: 'b' } })
  fireEvent.keyDown(screen.getByLabelText('Model'), { key: 'Enter' })
  expect(onList).toHaveBeenLastCalledWith(['a', 'b'])
  fireEvent.click(screen.getByRole('button', { name: 'Remove Model a', hidden: true }))
  expect(onList).toHaveBeenLastCalledWith(undefined)
  fireEvent.change(screen.getByLabelText('Streamed'), { target: { value: 'false' } })
  expect(onBool).toHaveBeenLastCalledWith(false)
  fireEvent.change(screen.getByLabelText('Latency at least'), { target: { value: '300' } })
  expect(onNum).toHaveBeenLastCalledWith(300)
})

test('primitives have no accessibility violations', async () => {
  const { container } = render(
    <div>
      <TimeRangePicker value={{}} onChange={() => {}} />
      <FilterMenu activeCount={1} onClear={() => {}}>
        <TextListFilter label="Model" values={['a']} onChange={() => {}} />
        <BooleanFilter label="Streamed" value={true} onChange={() => {}} />
        <NumberFilter label="Latency at least" value={5} onChange={() => {}} />
      </FilterMenu>
      <ContentNotStored />
    </div>,
  )
  const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } })
  expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([])
})

test('filter menu is a native popover opened by its button', () => {
  render(
    <FilterMenu activeCount={0}>
      <NumberFilter label="Latency at least" value={undefined} onChange={() => {}} />
    </FilterMenu>,
  )
  const button = screen.getByRole('button', { name: 'Filters' })
  const panel = document.querySelector<HTMLElement>('.filter-panel')!
  // The browser owns open state, light dismiss, Escape and focus return; jsdom has no popover engine.
  expect(panel.getAttribute('popover')).toBe('auto')
  expect(button.getAttribute('popovertarget')).toBe(panel.id)
  expect(button.getAttribute('aria-expanded')).toBeNull()
  // Button and panel share one anchor, and two menus do not share one.
  const anchor = (button as HTMLElement).style.getPropertyValue('anchor-name')
  expect(anchor).toMatch(/^--filters-/)
  expect((panel as HTMLElement).style.getPropertyValue('position-anchor')).toBe(anchor)
})

test('the chip filter ignores Enter while an IME is composing', () => {
  const onList = vi.fn()
  render(<TextListFilter label="Model" values={[]} onChange={onList} />)
  const input = screen.getByLabelText('Model')
  fireEvent.change(input, { target: { value: 'ni' } })
  // A composing Enter, as Chrome and Firefox report it, and as Safari reports it (keyCode 229).
  fireEvent.keyDown(input, { key: 'Enter', isComposing: true })
  fireEvent.keyDown(input, { key: 'Enter', keyCode: 229 })
  expect(onList).not.toHaveBeenCalled()
  fireEvent.keyDown(input, { key: 'Enter' })
  expect(onList).toHaveBeenCalledWith(['ni'])
})

test('an info note is a button that opens a popover note', () => {
  render(<InfoNote label="API-equivalent" note="The plan bills the person." />)
  const button = screen.getByRole('button', { name: 'API-equivalent' })
  const note = screen.getByRole('note', { hidden: true })
  expect(note.getAttribute('popover')).toBe('auto')
  expect(button.getAttribute('popovertarget')).toBe(note.id)
  expect(note.textContent).toBe('The plan bills the person.')
})
