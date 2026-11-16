import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory } from '@tanstack/react-router'
import axe from 'axe-core'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { App } from '../App'
import { createQueryClient } from '../queryClient'
import { createAppRouter } from '../router'
import { call } from './callLogRows.test'
import type { CallSummary } from './callLogRows'

class FakeEventSource {
  static CLOSED = 2
  static instances: FakeEventSource[] = []
  readyState = 0
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  closed = false
  private listeners = new Map<string, ((e: MessageEvent<string>) => void)[]>()
  constructor(public url: string) {
    FakeEventSource.instances.push(this)
  }
  addEventListener(type: string, fn: (e: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn])
  }
  close() {
    this.closed = true
  }
  open() {
    act(() => this.onopen?.())
  }
  fail() {
    act(() => this.onerror?.())
  }
  refuse() {
    this.readyState = FakeEventSource.CLOSED
    act(() => this.onerror?.())
  }
  // Several calls are delivered as one burst, in one act().
  finish(...calls: CallSummary[]) {
    act(() => {
      for (const c of calls) {
        for (const fn of this.listeners.get('call.finished') ?? []) {
          fn(new MessageEvent('call.finished', { data: JSON.stringify(c), lastEventId: c.id }))
        }
      }
    })
  }
}

const usageBody = {
  group_by: null,
  grain: 'hour',
  buckets: [
    {
      start: '2026-11-01T10:00:00Z',
      group: '',
      calls: 3,
      errors: 1,
      usage: { input_uncached: 10, cache_read: 20, cache_write_5m: 0, cache_write_1h: 0, output: 5, reasoning: 0 },
      cost_micro_usd: 2_500_000,
      unpriced_calls: 1,
      estimated_calls: 0,
      no_usage_calls: 0,
      latency_p50_ms: 1,
      latency_p95_ms: 1,
      latency_p99_ms: 1,
      cache_hit_ratio: 0.5,
    },
  ],
}

function list(calls: CallSummary[], over: object = {}) {
  return { calls, next_cursor: null, count: calls.length, count_capped: false, ...over }
}

function stubApi(respond: (url: string) => Response | Promise<Response>) {
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      urls.push(url)
      if (url.startsWith('/admin/usage')) return new Response(JSON.stringify(usageBody))
      return respond(url)
    }),
  )
  return urls
}

async function renderAt(path: string) {
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  const view = render(<App queryClient={queryClient} router={router} />)
  await waitFor(() => expect(screen.getByRole('main')).toBeTruthy())
  return { router, ...view }
}

const search = (router: { state: { location: { search: unknown } } }) =>
  router.state.location.search as Record<string, unknown>
const listUrls = (urls: string[]) => urls.filter((u) => u.startsWith('/admin/calls'))
const tableRows = () => within(screen.getByRole('table', { name: 'Calls' })).getAllByRole('row').slice(1)

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(480)
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1200)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    width: 1200, height: 480, top: 0, left: 0, right: 1200, bottom: 480, x: 0, y: 0, toJSON: () => ({}),
  })
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

test('shows rows, summary strip and capped count; a row opens V3 and a session opens V2', async () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-11-01T12:00:00Z'))
  stubApi(() =>
    new Response(
      JSON.stringify(
        list([call('c1', { session_key: 'sess/1', prompt_id: 'prompt-7', cost_micro_usd: null })], { count: 10000, count_capped: true }),
      ),
    ),
  )
  const { router, container } = await renderAt('/ui/calls')
  await screen.findByText('claude-code')
  const strip = screen.getByRole('group', { name: 'Summary' })
  expect(within(strip).getByText('10,000+')).toBeTruthy()
  expect(within(strip).getByText('$2.50')).toBeTruthy()
  expect(within(strip).getByText(/1 calls unpriced/)).toBeTruthy()
  expect(screen.getByRole('img', { name: /3 calls, 1 with errors/ })).toBeTruthy()
  expect(within(tableRows()[0]).getByText('prompt-7')).toBeTruthy()
  // Unknown cost is not zero.
  expect(within(tableRows()[0]).getAllByText('unknown').length).toBeGreaterThan(0)
  expect((await axe.run(container)).violations.filter((v) => v.impact === 'critical' || v.impact === 'serious')).toEqual([])

  const link = screen.getByRole('link', { name: 'sess/1' })
  expect(link.getAttribute('href')).toBe('/ui/sessions/sess%2F1')
  fireEvent.click(tableRows()[0])
  await waitFor(() => expect(router.state.location.pathname).toBe('/calls/c1'))
})

test('filters round trip through the URL search parameters', async () => {
  const urls = stubApi(() => new Response(JSON.stringify(list([call('c1')]))))
  const { router } = await renderAt('/ui/calls?model=claude-x&status=429&streamed=true&latency_ms_gte=250')
  await screen.findByText('claude-code')
  // URL to request.
  const first = new URL(listUrls(urls)[0], 'http://x')
  expect(first.searchParams.getAll('model')).toEqual(['claude-x'])
  expect(first.searchParams.getAll('status')).toEqual(['429'])
  expect(first.searchParams.get('streamed')).toBe('true')
  expect(first.searchParams.get('latency_ms_gte')).toBe('250')
  expect(first.searchParams.get('cursor')).toBeNull()

  // Menu to URL.
  fireEvent.click(screen.getByRole('button', { name: /Filters \(4\)/ }))
  fireEvent.change(screen.getByRole('textbox', { name: 'Client', hidden: true }), { target: { value: 'cursor' } })
  fireEvent.click(screen.getAllByRole('button', { name: 'Add', hidden: true })[1])
  await waitFor(() => expect(search(router).client).toEqual(['cursor']))
  fireEvent.change(screen.getByLabelText('Tool error'), { target: { value: 'true' } })
  await waitFor(() => expect(search(router).tool_error).toBe(true))
  fireEvent.click(screen.getByRole('button', { name: 'Remove Model claude-x', hidden: true }))
  await waitFor(() => expect(search(router).model).toBeUndefined())
  expect(search(router)).toMatchObject({ client: ['cursor'], status: [429], streamed: true, tool_error: true })
  await waitFor(() => {
    const last = new URL(listUrls(urls).at(-1)!, 'http://x')
    expect(last.searchParams.getAll('client')).toEqual(['cursor'])
    expect(last.searchParams.get('model')).toBeNull()
  })
  fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }))
  await waitFor(() => expect(search(router)).toEqual({}))
})

test('group by session folds consecutive calls and is kept in the URL', async () => {
  stubApi(() =>
    new Response(
      JSON.stringify(
        list([
          call('c1', { session_key: 's1', prompt_id: 'p1', cost_micro_usd: 1_000_000 }),
          call('c2', { session_key: 's1', prompt_id: 'p2', cost_micro_usd: 2_000_000 }),
          call('c3', { session_key: '' }),
          call('c4', { session_key: 's2', prompt_id: 'p9', cost_micro_usd: 1_000_000 }),
          call('c5', { session_key: 's2', cost_micro_usd: null }),
        ]),
      ),
    ),
  )
  const { router } = await renderAt('/ui/calls')
  await screen.findAllByText('claude-code')
  expect(tableRows()).toHaveLength(5)
  fireEvent.click(screen.getByLabelText('Group by session'))
  await waitFor(() => expect(search(router).group).toBe('session'))
  await waitFor(() => expect(tableRows()).toHaveLength(3))
  expect(screen.getAllByText('2 turns')).toHaveLength(2)
  expect(within(tableRows()[0]).getByText('$3.00')).toBeTruthy()
  expect(within(tableRows()[0]).getByText('2 prompts')).toBeTruthy()
  // A turn with no prompt id is not another prompt.
  expect(within(tableRows()[2]).getByText('p9')).toBeTruthy()
  // A turn with an unknown cost is not counted as zero.
  expect(within(tableRows()[2]).getByText('$1.00')).toBeTruthy()
  // The cell shows a compact marker; its accessible label spells it out.
  expect(within(tableRows()[2]).getByText('+1?')).toBeTruthy()
  expect(within(tableRows()[2]).getByLabelText('+1 unknown')).toBeTruthy()
  // The title keeps the full total readable when the column is narrow.
  expect(within(tableRows()[2]).getByTitle('$1.00 (+1 unknown)')).toBeTruthy()
  // A group row opens the session.
  fireEvent.click(tableRows()[0])
  await waitFor(() => expect(router.state.location.pathname).toBe('/sessions/s1'))
})

test('live toggle opens the stream with the filters, inserts at the top and caps at 500', async () => {
  stubApi(() => new Response(JSON.stringify(list([call('old')]))))
  const { router } = await renderAt('/ui/calls?model=claude-x&range=7d')
  await screen.findByText('claude-code')
  expect(FakeEventSource.instances).toHaveLength(0)
  fireEvent.click(screen.getByLabelText('Live'))
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
  expect(search(router).live).toBe(true)
  const es = FakeEventSource.instances[0]
  const url = new URL(es.url, 'http://x')
  expect(url.pathname).toBe('/admin/stream')
  expect(url.searchParams.getAll('model')).toEqual(['claude-x'])
  // A preset range is relative to now: no time bounds on the stream.
  expect(url.searchParams.get('from')).toBeNull()
  es.open()
  await screen.findByText('Live', { selector: '[role=status]' })

  es.finish(call('new1', { client: 'fresh-client' }))
  await screen.findByText('fresh-client')
  expect(within(tableRows()[0]).getByText('fresh-client')).toBeTruthy()

  es.finish(...Array.from({ length: 520 }, (_, i) => call(`live${i}`, { client: 'bulk' })))
  // 500 live rows stay; the paged row is still there below them.
  const body = screen.getByRole('table', { name: 'Calls' })
  await waitFor(() => expect(body.getAttribute('aria-rowcount')).toBe('502'))

  fireEvent.click(screen.getByLabelText('Live'))
  await waitFor(() => expect(es.closed).toBe(true))
  await waitFor(() => expect(body.getAttribute('aria-rowcount')).toBe('2'))

  // Live again with the same filters: a new stream, no rows or status from the old one.
  fireEvent.click(screen.getByLabelText('Live'))
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2))
  expect(screen.getByRole('status').textContent).toBe('Connecting…')
  await waitFor(() => expect(body.getAttribute('aria-rowcount')).toBe('2'))
})

test('turning live on reloads the first page up to now', async () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-11-01T10:00:00Z'))
  const urls = stubApi(() => new Response(JSON.stringify(list([call('old')]))))
  await renderAt('/ui/calls')
  await screen.findByText('claude-code')
  expect(new URL(listUrls(urls)[0], 'http://x').searchParams.get('to')).toBe('2026-11-01T10:00:00.000Z')
  vi.setSystemTime(new Date('2026-11-01T11:00:00Z'))
  fireEvent.click(screen.getByLabelText('Live'))
  await waitFor(() =>
    expect(new URL(listUrls(urls).at(-1)!, 'http://x').searchParams.get('to')).toBe('2026-11-01T11:00:00.000Z'),
  )
})

test('changing the range or a filter re-resolves the window against now', async () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-11-01T10:00:00Z'))
  const urls = stubApi(() => new Response(JSON.stringify(list([call('old')]))))
  const { router } = await renderAt('/ui/calls')
  await screen.findByText('claude-code')
  vi.setSystemTime(new Date('2026-11-01T12:00:00Z'))
  fireEvent.click(screen.getByRole('button', { name: '1h' }))
  await waitFor(() => expect(search(router).range).toBe('1h'))
  await waitFor(() => {
    const params = new URL(listUrls(urls).at(-1)!, 'http://x').searchParams
    expect(params.get('from')).toBe('2026-11-01T11:00:00.000Z')
    expect(params.get('to')).toBe('2026-11-01T12:00:00.000Z')
  })
  vi.setSystemTime(new Date('2026-11-01T12:30:00Z'))
  fireEvent.click(screen.getByLabelText('Group by session'))
  await waitFor(() =>
    expect(new URL(listUrls(urls).at(-1)!, 'http://x').searchParams.get('to')).toBe('2026-11-01T12:30:00.000Z'),
  )
})

test('a stream the browser closes shows it and can be retried', async () => {
  const urls = stubApi(() => new Response(JSON.stringify(list([call('old')]))))
  await renderAt('/ui/calls?live=true')
  await screen.findByText('claude-code')
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
  FakeEventSource.instances[0].refuse()
  await screen.findByText('Stream closed')
  const before = listUrls(urls).length
  fireEvent.click(screen.getByRole('button', { name: 'Retry stream' }))
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2))
  expect(FakeEventSource.instances[0].closed).toBe(true)
  FakeEventSource.instances[1].open()
  await screen.findByText('Live', { selector: '[role=status]' })
  await waitFor(() => expect(listUrls(urls).length).toBe(before + 1))
})

test('reconnect refetches the first page and de-duplicates live rows by call id', async () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-11-01T10:00:00Z'))
  let page = [call('old')]
  const urls = stubApi(() => new Response(JSON.stringify(list(page))))
  await renderAt('/ui/calls?live=true')
  await screen.findByText('claude-code')
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
  const es = FakeEventSource.instances[0]
  es.open()
  es.finish(call('missed', { client: 'dup-client' }))
  await screen.findByText('dup-client')
  const before = listUrls(urls).length

  // The stream drops, the call is also in the refetched first page, and the stream reopens.
  page = [call('missed', { client: 'dup-client' }), call('old')]
  es.fail()
  await screen.findByText('Reconnecting…')
  vi.setSystemTime(new Date('2026-11-01T10:32:00Z'))
  es.open()
  await waitFor(() => expect(listUrls(urls).length).toBe(before + 1))
  const refetched = new URL(listUrls(urls).at(-1)!, 'http://x').searchParams
  expect(refetched.get('cursor')).toBeNull()
  // The reloaded page reaches the reconnect time, not the mount time.
  expect(refetched.get('to')).toBe('2026-11-01T10:32:00.000Z')
  await waitFor(() => expect(screen.getByRole('table', { name: 'Calls' }).getAttribute('aria-rowcount')).toBe('3'))
  expect(screen.getAllByText('dup-client')).toHaveLength(1)
  // A first open without an error does not refetch.
  expect(FakeEventSource.instances).toHaveLength(1)
})

test('the volume chart has one slot per interval, empty intervals included', async () => {
  stubApi(() => new Response(JSON.stringify(list([call('c1')]))))
  const { container } = await renderAt('/ui/calls?from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z')
  await screen.findByText('claude-code')
  await screen.findByRole('img', { name: /3 calls, 1 with errors, in 24 intervals/ })
  const bars = container.querySelectorAll('.volume-chart rect.bar')
  expect(bars).toHaveLength(1)
  // The 10:00 bucket is the eleventh of 24 hours.
  expect(bars[0].getAttribute('x')).toBe('250')
})

test('virtualises 5,000 rows', async () => {
  const many = Array.from({ length: 5000 }, (_, i) => call(`c${i}`, { client: `client-${i}` }))
  stubApi(() => new Response(JSON.stringify(list(many, { count: 5000 }))))
  await renderAt('/ui/calls')
  await screen.findByText('client-0')
  const body = screen.getByRole('table', { name: 'Calls' })
  expect(body.getAttribute('aria-rowcount')).toBe('5001')
  expect(tableRows().length).toBeLessThan(40)
  expect(screen.queryByText('client-4999')).toBeNull()
})

test('empty and error states', async () => {
  let fail = true
  stubApi(() =>
    fail
      ? new Response(JSON.stringify({ error: { type: 'internal_error', message: 'The server failed' } }), { status: 500 })
      : new Response(JSON.stringify(list([]))),
  )
  await renderAt('/ui/calls?model=none')
  const alert = await screen.findByRole('alert', {}, { timeout: 5000 })
  expect(alert.textContent).toContain('The server failed')
  fail = false
  fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
  await screen.findByText('No calls match these filters.', {}, { timeout: 5000 })
})

test('an unfiltered empty log says so', async () => {
  stubApi(() => new Response(JSON.stringify(list([]))))
  await renderAt('/ui/calls')
  await screen.findByText('No calls in the last 24h.')
})

test('rows move with j, k and the arrow keys, and Enter opens the focused call', async () => {
  stubApi(() => new Response(JSON.stringify(list([call('c1'), call('c2'), call('c3')]))))
  const { router } = await renderAt('/ui/calls')
  await screen.findAllByText('claude-code')
  const rows = tableRows()
  // One row is in the tab order (roving tab index).
  expect(rows.map((r) => r.tabIndex)).toEqual([0, -1, -1])
  rows[0].focus()
  fireEvent.keyDown(rows[0], { key: 'j' })
  expect(document.activeElement).toBe(tableRows()[1])
  fireEvent.keyDown(tableRows()[1], { key: 'ArrowDown' })
  expect(document.activeElement).toBe(tableRows()[2])
  expect(tableRows().map((r) => r.tabIndex)).toEqual([-1, -1, 0])
  fireEvent.keyDown(tableRows()[2], { key: 'k' })
  expect(document.activeElement).toBe(tableRows()[1])
  fireEvent.keyDown(tableRows()[1], { key: 'ArrowUp' })
  expect(document.activeElement).toBe(tableRows()[0])
  fireEvent.keyDown(tableRows()[0], { key: 'End' })
  expect(document.activeElement).toBe(tableRows()[2])
  fireEvent.keyDown(tableRows()[2], { key: 'Enter' })
  await waitFor(() => expect(router.state.location.pathname).toBe('/calls/c3'))
})

test('Enter on a group row opens the session', async () => {
  stubApi(() => new Response(JSON.stringify(list([call('c1', { session_key: 's1' }), call('c2', { session_key: 's1' })]))))
  const { router } = await renderAt('/ui/calls?group=%22session%22')
  await screen.findByText('2 turns')
  tableRows()[0].focus()
  fireEvent.keyDown(tableRows()[0], { key: 'Enter' })
  await waitFor(() => expect(router.state.location.pathname).toBe('/sessions/s1'))
})

test('numbers are right-aligned and status shows as a badge with the error class in its text', async () => {
  stubApi(() =>
    new Response(JSON.stringify(list([call('ok'), call('bad', { status: 429, error_class: 'rate_limited' }), call('gone', { status: 0 })]))),
  )
  await renderAt('/ui/calls')
  await screen.findAllByText('claude-code')
  const [ok, bad, gone] = tableRows().map((r) => within(r))
  expect(ok.getByText('200').className).toBe('badge badge-ok')
  expect(bad.getByText('429 · rate_limited').className).toBe('badge badge-error')
  expect(gone.getAllByText('unknown').length).toBeGreaterThan(0)
  expect(screen.getByRole('columnheader', { name: /Latency/ }).className).toContain('num')
  expect(ok.getByText('100 ms').closest('[role=cell]')?.className).toContain('num')
  // Model and client are subtle tags.
  expect(ok.getByText('claude-code').className).toBe('tag')
  expect(ok.getByText('m').className).toBe('tag mono')
})

test('a sub-cent cost and a long error badge keep their full value in a title', async () => {
  stubApi(() =>
    new Response(JSON.stringify(list([call('bad', { status: 502, error_class: 'upstream_error', cost_micro_usd: 7_512 })]))),
  )
  await renderAt('/ui/calls')
  await screen.findAllByText('claude-code')
  const row = within(tableRows()[0])
  expect(row.getByText('502 · upstream_error').getAttribute('title')).toBe('502 · upstream_error')
  expect(row.getByTitle('$0.007512').textContent).toBe('$0.007512')
})
