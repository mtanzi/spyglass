import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory } from '@tanstack/react-router'
import axe from 'axe-core'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { App } from '../App'
import { createQueryClient } from '../queryClient'
import { createAppRouter } from '../router'
import { toolCall, toolStat } from './toolsData.test'
import type { ToolCall, ToolStat } from './toolsData'

function stubApi({
  stats = [] as ToolStat[],
  calls = [] as ToolCall[],
  fail = () => false,
}: { stats?: ToolStat[]; calls?: ToolCall[]; fail?: () => boolean } = {}) {
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      urls.push(url)
      if (fail()) {
        return new Response(JSON.stringify({ error: { type: 'internal', message: 'The server failed' } }), { status: 500 })
      }
      if (url.startsWith('/admin/tool-calls/summary')) return new Response(JSON.stringify({ tools: stats }))
      if (url.startsWith('/admin/tool-calls')) return new Response(JSON.stringify({ tool_calls: calls, next_cursor: null }))
      return new Response(JSON.stringify({ error: { type: 'not_found', message: 'no route' } }), { status: 404 })
    }),
  )
  return urls
}

async function renderAt(path: string) {
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  const view = render(<App queryClient={queryClient} router={router} />)
  await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Tools' })).toBeTruthy())
  return { router, ...view }
}

const search = (router: { state: { location: { search: unknown } } }) =>
  router.state.location.search as Record<string, unknown>
const rowsOf = (name: string) => within(screen.getByRole('table', { name })).getAllByRole('row').slice(1)
const listUrls = (urls: string[]) => urls.filter((u) => u.startsWith('/admin/tool-calls?'))
const summaryUrls = (urls: string[]) => urls.filter((u) => u.startsWith('/admin/tool-calls/summary'))

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(480)
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1200)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    width: 1200, height: 480, top: 0, left: 0, right: 1200, bottom: 480, x: 0, y: 0, toJSON: () => ({}),
  })
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const stats = [
  toolStat('Bash', { count: 5, errors: 1, error_rate: 0.2, duration_p95_ms: 3000 }),
  toolStat('Read', { count: 20, duration_p95_ms: 50 }),
  toolStat('Read', { agent_type: 'reviewer', count: 8, duration_p95_ms: 70 }),
  toolStat('Task', { count: 2, duration_p50_ms: 0, duration_p95_ms: 0, total_duration_ms: 0 }),
]

test('the summary sorts by count first and keeps a header sort in the URL', async () => {
  stubApi({ stats })
  const { router } = await renderAt('/ui/tools')
  await waitFor(() => expect(rowsOf('Tool summary')).toHaveLength(4))
  const names = () => rowsOf('Tool summary').map((r) => within(r).getAllByRole('cell')[0].textContent)
  const counts = () => rowsOf('Tool summary').map((r) => within(r).getAllByRole('cell')[2].textContent)
  expect(counts()).toEqual(['20', '8', '5', '2'])
  expect(screen.getByRole('columnheader', { name: /Count/ }).getAttribute('aria-sort')).toBe('descending')
  // Split by agent type: one row per tool and agent type.
  expect(names()).toEqual(['Read', 'Read', 'Bash', 'Task'])

  fireEvent.click(screen.getByRole('button', { name: 'p95' }))
  await waitFor(() => expect(search(router).sort).toBe('p95'))
  // A row with no duration reads unknown and sorts last.
  await waitFor(() => expect(names()).toEqual(['Bash', 'Read', 'Read', 'Task']))
  // p50, p95 and total, plus the agent type the row does not have.
  expect(within(rowsOf('Tool summary')[3]).getAllByText('unknown').length).toBe(4)
  fireEvent.click(screen.getByRole('button', { name: /p95/ }))
  await waitFor(() => expect(search(router).order).toBe('asc'))
  await waitFor(() => expect(names()).toEqual(['Read', 'Read', 'Bash', 'Task']))

  fireEvent.click(screen.getByRole('button', { name: 'Tool' }))
  await waitFor(() => expect(search(router)).toMatchObject({ sort: 'name' }))
  expect(search(router).order).toBeUndefined()
  await waitFor(() => expect(names()).toEqual(['Bash', 'Read', 'Read', 'Task']))
})

test('a sort in the URL is applied on load, and errors show as a badge with text', async () => {
  stubApi({ stats })
  await renderAt('/ui/tools?sort=error_rate')
  await waitFor(() => expect(rowsOf('Tool summary')).toHaveLength(4))
  const first = within(rowsOf('Tool summary')[0])
  expect(first.getByText('Bash')).toBeTruthy()
  const badge = first.getByText('20.0%')
  expect(badge.className).toBe('badge badge-error')
  // The time range and the filters sit in a <search> landmark (jsdom has no `search` role yet).
  const search = document.querySelector<HTMLElement>('search')!
  expect(within(search).getByRole('button', { name: 'Filters' })).toBeTruthy()
  expect(within(search).getByRole('button', { name: '24h' })).toBeTruthy()
  // The failed count is visible text beside the badge, not a title.
  expect(first.getByText('1 failed')).toBeTruthy()
  const strip = screen.getByRole('group', { name: 'Summary' })
  expect(within(strip).getByText('35')).toBeTruthy()
  expect(within(strip).getByText('2.9%')).toBeTruthy()
})

test('a summary row filters the list to its tool', async () => {
  const urls = stubApi({ stats })
  const { router } = await renderAt('/ui/tools')
  await waitFor(() => expect(rowsOf('Tool summary')).toHaveLength(4))
  fireEvent.click(rowsOf('Tool summary')[2])
  await waitFor(() => expect(search(router).tool).toEqual(['Bash']))
  await waitFor(() => expect(new URL(listUrls(urls).at(-1)!, 'http://x').searchParams.getAll('tool')).toEqual(['Bash']))
})

test('filters round trip through the URL to both endpoints', async () => {
  const urls = stubApi({ stats, calls: [toolCall('t1')] })
  const { router } = await renderAt(
    '/ui/tools?tool=Read&is_error=true&duration_ms_gte=500&arg_bytes_gte=10&result_bytes_gte=20&session=s%2F1',
  )
  await waitFor(() => expect(rowsOf('Tool calls')).toHaveLength(1))
  for (const u of [listUrls(urls)[0], summaryUrls(urls)[0]]) {
    const p = new URL(u, 'http://x').searchParams
    expect(p.getAll('tool')).toEqual(['Read'])
    expect(p.get('is_error')).toBe('true')
    expect(p.get('duration_ms_gte')).toBe('500')
    expect(p.get('arg_bytes_gte')).toBe('10')
    expect(p.get('result_bytes_gte')).toBe('20')
    expect(p.get('session')).toBe('s/1')
  }
  expect(new URL(listUrls(urls)[0], 'http://x').searchParams.get('limit')).toBe('100')
  expect(new URL(summaryUrls(urls)[0], 'http://x').searchParams.get('limit')).toBeNull()

  fireEvent.click(screen.getByRole('button', { name: /Filters \(6\)/ }))
  fireEvent.change(screen.getByLabelText('Error'), { target: { value: 'false' } })
  await waitFor(() => expect(search(router).is_error).toBe(false))
  fireEvent.click(screen.getByRole('checkbox', { name: 'Unmatched only' }))
  await waitFor(() => expect(search(router).status).toEqual(['use_only', 'result_only']))
  fireEvent.click(screen.getByRole('button', { name: 'Remove Tool name Read', hidden: true }))
  await waitFor(() => expect(search(router).tool).toBeUndefined())
  await waitFor(() => {
    const p = new URL(listUrls(urls).at(-1)!, 'http://x').searchParams
    expect(p.getAll('status')).toEqual(['use_only', 'result_only'])
    expect(p.get('is_error')).toBe('false')
    expect(p.get('tool')).toBeNull()
  })
  fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }))
  await waitFor(() => expect(search(router)).toEqual({}))
})

test('the unmatched filter selects both unmatched halves and its rows say so', async () => {
  const urls = stubApi({
    calls: [
      toolCall('pending', { status: 'pending', result_call_id: null, result_ts: null, is_error: null, result_bytes: 0, duration_ms: null, duration_source: null }),
      toolCall('use', { status: 'use_only', result_call_id: null, result_ts: null, is_error: null, result_bytes: 0, duration_ms: null, duration_source: null }),
      toolCall('res', { status: 'result_only', name: '', use_call_id: null, use_ts: null, arg_bytes: 0, is_error: true, duration_ms: null, duration_source: null }),
    ],
  })
  const { router } = await renderAt('/ui/tools')
  await waitFor(() => expect(rowsOf('Tool calls')).toHaveLength(3))
  const [pending, useOnly, resultOnly] = rowsOf('Tool calls').map((r) => within(r))
  expect(pending.getByText('Pending')).toBeTruthy()
  expect(useOnly.getByText('No result')).toBeTruthy()
  expect(useOnly.queryByText('Success')).toBeNull()
  // A result with no use: its outcome is known, its name and argument size are not.
  expect(resultOnly.getByText('No use')).toBeTruthy()
  expect(resultOnly.getByText('Error').className).toBe('badge badge-error')
  expect(resultOnly.getByText('no request')).toBeTruthy()
  expect(resultOnly.getAllByText('unknown').length).toBeGreaterThanOrEqual(3)
  // No result means no result size, not 0 B.
  expect(useOnly.queryByText('0 B')).toBeNull()

  fireEvent.click(screen.getByLabelText('Unmatched only'))
  await waitFor(() => expect(search(router).status).toEqual(['use_only', 'result_only']))
  await waitFor(() =>
    expect(new URL(listUrls(urls).at(-1)!, 'http://x').searchParams.getAll('status')).toEqual(['use_only', 'result_only']),
  )
  fireEvent.click(screen.getByLabelText('Unmatched only'))
  await waitFor(() => expect(search(router).status).toBeUndefined())
})

test('a stale unmatched URL keeps only valid states', async () => {
  stubApi()
  const { router } = await renderAt('/ui/tools?status=use_only&status=bogus&status=result_only')
  await waitFor(() => expect(search(router).status).toEqual(['use_only', 'result_only']))
  expect((screen.getByLabelText('Unmatched only') as HTMLInputElement).checked).toBe(true)
})

test('the tools URL drops params that are not ADR 0034 filters', async () => {
  const urls = stubApi()
  const { router } = await renderAt('/ui/tools?agent=a1&source=server&status=pending&status=paired&bogus=1&session=s1')
  await waitFor(() => expect(search(router)).toEqual({ session: 's1' }))
  expect((screen.getByLabelText('Unmatched only') as HTMLInputElement).checked).toBe(false)
  await waitFor(() => expect(listUrls(urls).length * summaryUrls(urls).length).toBeGreaterThan(0))
  for (const u of [...listUrls(urls), ...summaryUrls(urls)]) {
    const p = new URL(u, 'http://x').searchParams
    expect(p.get('session')).toBe('s1')
    for (const k of ['agent', 'source', 'status', 'bogus']) expect(p.has(k)).toBe(false)
  }
})

test('list rows show sizes and the duration source, and link to V3 and V2', async () => {
  stubApi({
    calls: [
      toolCall('h1', { session_key: 'sess/1' }),
      toolCall('e1', { duration_source: 'estimated', duration_ms: 400, name: 'Grep' }),
    ],
  })
  const { router, container } = await renderAt('/ui/tools')
  await waitFor(() => expect(rowsOf('Tool calls')).toHaveLength(2))
  const [header, estimated] = rowsOf('Tool calls').map((r) => within(r))
  expect(header.getByText('120 B')).toBeTruthy()
  expect(header.getByText('2.0 KiB')).toBeTruthy()
  expect(header.getByText('1.50 s')).toBeTruthy()
  // The source word opens a note that explains it.
  expect(header.getByRole('button', { name: 'header' }).getAttribute('popovertarget')).toBeTruthy()
  expect(header.getByRole('note', { hidden: true }).textContent).toMatch(/prev_tool_durations/)
  expect(estimated.getByRole('note', { hidden: true }).textContent).toMatch(/gap between the use and the result/)
  expect(header.getByText('Success').className).toBe('badge badge-ok')
  expect(screen.getByRole('link', { name: 'sess/1' }).getAttribute('href')).toBe('/ui/sessions/sess%2F1')
  expect(header.getByRole('link', { name: 'Result' }).getAttribute('href')).toBe(
    '/ui/calls/0190a1b2-0000-7000-8000-000000000002?tab=tools',
  )
  expect(screen.getByRole('note').textContent).toBe('Content is not stored')
  const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } })
  expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([])

  // Keyboard: j moves, Enter opens the requesting call.
  const rows = rowsOf('Tool calls')
  rows[0].focus()
  fireEvent.keyDown(rows[0], { key: 'j' })
  expect(document.activeElement).toBe(rowsOf('Tool calls')[1])
  fireEvent.keyDown(rowsOf('Tool calls')[1], { key: 'Enter' })
  await waitFor(() => expect(router.state.location.pathname).toBe('/calls/0190a1b2-0000-7000-8000-000000000001'))
})

test('an unfiltered empty page says so in both tables', async () => {
  stubApi()
  await renderAt('/ui/tools')
  await waitFor(() => expect(screen.getAllByText('No tool calls in the last 24h.')).toHaveLength(2))
})

test('a filtered empty page and an error with retry', async () => {
  let failing = true
  stubApi({ fail: () => failing })
  await renderAt('/ui/tools?tool=Nope')
  const alerts = await screen.findAllByRole('alert', {}, { timeout: 5000 })
  expect(alerts[0].textContent).toContain('The server failed')
  failing = false
  for (const a of screen.getAllByRole('alert')) fireEvent.click(within(a).getByRole('button', { name: 'Retry' }))
  await waitFor(() => expect(screen.getAllByText('No tool calls match these filters.')).toHaveLength(2), { timeout: 5000 })
})
