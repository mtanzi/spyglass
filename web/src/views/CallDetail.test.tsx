import { render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory } from '@tanstack/react-router'
import axe from 'axe-core'
import { afterEach, expect, test, vi } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { App } from '../App'
import { createQueryClient } from '../queryClient'
import { createAppRouter } from '../router'
import type { CallDetail } from './CallDetail'
import { buildToolRows, fetchToolPairs } from './toolPairs'
import type { ToolCall } from './toolPairs'

const ID = '0190a1b2-0000-7000-8000-000000000001'
const OTHER = '0190a1b2-0000-7000-8000-000000000002'

function detail(over: Partial<CallDetail> = {}): CallDetail {
  return {
    id: ID,
    ts: '2026-11-01T10:00:00Z',
    key: 'fm:task-1',
    client: 'claude-code',
    client_raw: '',
    client_request_id: '',
    session_key: 'sess/1:a',
    agent_id: 'agent-1',
    parent_agent_id: 'agent-0',
    agent_type: '',
    prompt_id: 'p-1',
    request_class: '',
    compaction: false,
    context_compacted: false,
    trace_id: '',
    family: 'anthropic',
    endpoint: '/v1/messages',
    auth_mode: 'proxy_key',
    provider: 'anthropic',
    model: 'claude-x',
    upstream_request_id: 'req_1',
    status: 200,
    error_class: '',
    error_type: '',
    stop_reason: 'end_turn',
    streamed: true,
    ttfb_ms: 10,
    ttft_ms: 120,
    duration_ms: 2500,
    ended_at: '2026-11-01T10:00:02Z',
    usage: { input_uncached: 10, cache_read: 20, cache_write_5m: 30, cache_write_1h: 0, output: 40, reasoning: 5 },
    usage_raw: { input_tokens: 10 },
    usage_estimated: false,
    extraction_partial: false,
    cost_micro_usd: 1234,
    pricing_version: '2026-01-01',
    prev_tool_durations: '',
    tool_uses: [],
    tool_results: [],
    ...over,
  }
}

function pair(over: Partial<ToolCall>): ToolCall {
  return {
    tool_call_id: 't1',
    name: 'Read',
    source: 'client',
    status: 'paired',
    session_key: 'sess/1:a',
    agent_id: 'agent-1',
    use_call_id: ID,
    result_call_id: OTHER,
    use_ts: null,
    result_ts: null,
    duration_ms: 300,
    duration_source: 'header',
    is_error: false,
    arg_bytes: 100,
    result_bytes: 2048,
    ...over,
  }
}

function stubApi(call: CallDetail, pairs: ToolCall[] = []) {
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      urls.push(url)
      if (url.startsWith('/admin/calls/')) return new Response(JSON.stringify(call))
      return new Response(JSON.stringify({ tool_calls: pairs, next_cursor: null }))
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

afterEach(() => vi.unstubAllGlobals())

test('overview shows who, outcome and links the session to V2', async () => {
  stubApi(detail())
  await renderAt(`/ui/calls/${ID}`)
  await screen.findByText('claude-code')
  expect(screen.getByRole('link', { name: 'sess/1:a' }).getAttribute('href')).toBe(
    '/ui/sessions/sess%2F1%3Aa',
  )
  expect(screen.getByText('2.50 s')).toBeTruthy()
  expect(screen.getByText('120 ms')).toBeTruthy()
  expect(screen.getByText('end_turn')).toBeTruthy()
  expect(screen.queryByText('Usage estimated')).toBeNull()
  expect(screen.getAllByText('Content is not stored')).toHaveLength(1)
})

test('partial data renders as unknown, with visible badges', async () => {
  stubApi(
    detail({
      session_key: '',
      upstream_request_id: '',
      error_type: '',
      stop_reason: '',
      ttft_ms: 0,
      status: 0,
      usage_estimated: true,
      extraction_partial: true,
      cost_micro_usd: null,
      usage_raw: null,
      pricing_version: '',
    }),
  )
  const { router } = await renderAt(`/ui/calls/${ID}`)
  await screen.findByText('Usage estimated')
  expect(screen.getByText('Extraction partial')).toBeTruthy()
  expect(screen.queryByRole('link', { name: /sess/ })).toBeNull()
  // Status, error type and stop reason of the response; the time to first token.
  expect(within(screen.getByRole('article', { name: 'Response' })).getAllByText('unknown').length).toBeGreaterThanOrEqual(3)
  expect(within(screen.getByRole('region', { name: 'Timing' })).getAllByText('unknown')).toHaveLength(1)
  await router.navigate({ to: '/calls/$id', params: { id: ID }, search: { tab: 'usage' } })
  await screen.findByRole('heading', { name: 'Usage and cost' })
  const usage = within(screen.getByRole('region', { name: 'Usage and cost' }))
  expect(usage.getByText('Cost', { selector: 'dt' }).nextSibling?.textContent).toBe('unknown')
  expect(usage.getByText('Pricing version').nextSibling?.textContent).toBe('unknown')
  expect(screen.getByText('Raw usage').parentElement?.textContent).toContain('unknown')
})

test('the tab is a search parameter: URL selects it and tab links set it', async () => {
  stubApi(detail())
  const { router } = await renderAt(`/ui/calls/${ID}?tab=%22usage%22`)
  await screen.findByRole('heading', { name: 'Usage and cost' })
  expect(screen.getByRole('link', { name: 'Usage and cost' }).getAttribute('aria-current')).toBe('page')
  const usage = within(screen.getByRole('region', { name: 'Usage and cost' }))
  expect(usage.getByText('Cache read').closest('table')?.textContent).toContain('20')
  expect(usage.getByText('$0.001234')).toBeTruthy()
  expect(usage.getByText('2026-01-01')).toBeTruthy()
  expect(screen.getByText(/input_tokens/)).toBeTruthy()
  const tabs = within(screen.getByRole('navigation', { name: 'Call detail tabs' }))
  expect(tabs.getByRole('link', { name: 'Overview' }).getAttribute('href')).toBe(`/ui/calls/${ID}`)
  expect(tabs.getByRole('link', { name: 'Tools' }).getAttribute('href')).toBe(`/ui/calls/${ID}?tab=tools`)
  tabs.getByRole('link', { name: 'Tools' }).click()
  await waitFor(() => expect(router.state.location.search).toEqual({ tab: 'tools' }))
})

test('an unknown tab value degrades to overview', async () => {
  stubApi(detail())
  await renderAt(`/ui/calls/${ID}?tab=%22nope%22`)
  await screen.findByRole('heading', { name: 'Exchange' })
  const tabs = within(screen.getByRole('navigation', { name: 'Call detail tabs' }))
  expect(tabs.getByRole('link', { name: 'Overview' }).getAttribute('aria-current')).toBe('page')
})

const exchangeBlocks = () =>
  within(screen.getByRole('region', { name: 'Exchange' }))
    .getAllByRole('article')
    .map((r) => r.getAttribute('aria-label'))

test('the overview renders the exchange as a thread of system, user, assistant, tool call blocks', async () => {
  stubApi(
    detail({
      context_compacted: true,
      status: 529,
      error_class: 'overloaded',
      tool_results: [{ source: 'client', tool_call_id: 't9', is_error: true, result_bytes: 10 }],
      tool_uses: [
        { source: 'client', tool_call_id: 't1', name: 'Read', arg_bytes: 100 },
        { source: 'client', tool_call_id: 't2', name: 'Bash', arg_bytes: 2048 },
      ],
    }),
  )
  await renderAt(`/ui/calls/${ID}`)
  await screen.findByRole('heading', { name: 'Exchange' })
  // Request side first, then the response and the tools it asked for. No standalone result blocks.
  expect(exchangeBlocks()).toEqual(['Context compacted', 'Request', 'Response', 'Tool call Read', 'Tool call Bash'])
  expect(screen.queryByRole('article', { name: /Tool result/ })).toBeNull()
  const request = within(screen.getByRole('article', { name: 'Request' }))
  expect(request.getByText('p-1')).toBeTruthy()
  expect(request.getByText('60')).toBeTruthy()
  const response = screen.getByRole('article', { name: 'Response' })
  expect(response.className).toContain('block-assistant')
  expect(response.className).toContain('has-error')
  // The error is in the badge text, not in colour alone.
  expect(within(response).getByText('529 · overloaded').className).toContain('badge-error')
  expect(within(response).getByText('end_turn')).toBeTruthy()
  expect(within(screen.getByRole('article', { name: 'Tool call Bash' })).getByText('2.0 KiB')).toBeTruthy()
  expect(screen.getAllByText('Content is not stored')).toHaveLength(1)
})

test('a tool call shows the outcome and size of its result, or why none is known', async () => {
  stubApi(
    detail({
      tool_uses: [
        { source: 'client', tool_call_id: 't1', name: 'Read', arg_bytes: 100 },
        { source: 'client', tool_call_id: 't2', name: 'Bash', arg_bytes: 50 },
        { source: 'client', tool_call_id: 't3', name: 'Grep', arg_bytes: 5 },
        { source: 'client', tool_call_id: 't4', name: 'Edit', arg_bytes: 5 },
        { source: 'client', tool_call_id: 't5', name: 'Glob', arg_bytes: 5 },
      ],
    }),
    [
      pair({}),
      pair({ tool_call_id: 't2', name: 'Bash', is_error: true, result_bytes: 10 }),
      pair({ tool_call_id: 't3', name: 'Grep', status: 'pending', result_call_id: null, is_error: null, result_bytes: 0 }),
      pair({ tool_call_id: 't4', name: 'Edit', status: 'use_only', result_call_id: null, is_error: null, result_bytes: 0 }),
    ],
  )
  await renderAt(`/ui/calls/${ID}`)
  await screen.findByRole('heading', { name: 'Exchange' })
  fireEvent.click(screen.getByRole('button', { name: 'Show 2 more tool calls' }))
  const block = (name: string) => within(screen.getByRole('article', { name: `Tool call ${name}` }))
  await waitFor(() => expect(block('Read').getByText('Success')).toBeTruthy())
  expect(block('Read').getByText('2.0 KiB')).toBeTruthy()
  expect(block('Read').getByText('t1')).toBeTruthy()
  expect(block('Bash').getByText('Error').className).toContain('badge-error')
  expect(screen.getByRole('article', { name: 'Tool call Bash' }).className).toContain('has-error')
  expect(block('Bash').getByText('10 B')).toBeTruthy()
  expect(block('Grep').getByText('Pending')).toBeTruthy()
  expect(block('Edit').getByText('No result')).toBeTruthy()
  // No pair returned for the use: outcome and result size are unknown.
  expect(block('Glob').getAllByText('unknown')).toHaveLength(2)
  expect(screen.queryByRole('article', { name: /Tool result/ })).toBeNull()
})

test('a long run of tool calls collapses behind a button', async () => {
  stubApi(
    detail({
      tool_uses: Array.from({ length: 6 }, (_, i) => ({
        source: 'client',
        tool_call_id: `t${i}`,
        name: `Tool${i}`,
        arg_bytes: 1,
      })),
    }),
  )
  await renderAt(`/ui/calls/${ID}`)
  await screen.findByRole('heading', { name: 'Exchange' })
  expect(exchangeBlocks().filter((n) => n?.startsWith('Tool call'))).toEqual(['Tool call Tool0', 'Tool call Tool1', 'Tool call Tool2'])
  const more = screen.getByRole('button', { name: 'Show 3 more tool calls' })
  expect(more.getAttribute('aria-expanded')).toBe('false')
  fireEvent.click(more)
  expect(exchangeBlocks().filter((n) => n?.startsWith('Tool call'))).toHaveLength(6)
  const fewer = screen.getByRole('button', { name: 'Show fewer tool calls' })
  expect(fewer.getAttribute('aria-expanded')).toBe('true')
})

test('the timing bar draws time to first byte, first token and duration on the duration', async () => {
  stubApi(detail())
  const { container } = await renderAt(`/ui/calls/${ID}`)
  await screen.findByRole('heading', { name: 'Timing' })
  const timing = within(screen.getByRole('region', { name: 'Timing' }))
  expect(timing.getByText('10 ms')).toBeTruthy()
  expect(timing.getByText('120 ms')).toBeTruthy()
  expect(timing.getByText('2.50 s')).toBeTruthy()
  const width = (cls: string) => (container.querySelector(`.timing .${cls}`) as HTMLElement).style.width
  expect(width('bar-ttfb')).toBe('0.4%')
  expect(width('bar-ttft')).toBe('4.8%')
  expect(width('bar-total')).toBe('100%')
})

test('the side panel shows model, key, session link, tokens, cache and cost on every tab', async () => {
  stubApi(detail())
  const { router } = await renderAt(`/ui/calls/${ID}`)
  await screen.findByRole('complementary', { name: 'Call summary' })
  for (const tab of [{}, { tab: 'tools' as const }, { tab: 'usage' as const }]) {
    await router.navigate({ to: '/calls/$id', params: { id: ID }, search: tab })
    const panel = within(await screen.findByRole('complementary', { name: 'Call summary' }))
    expect(panel.getByText('claude-x')).toBeTruthy()
    expect(panel.getByText('fm:task-1')).toBeTruthy()
    expect(panel.getByRole('link', { name: 'sess/1:a' })).toBeTruthy()
    // 60 tokens in, 20 of them cache reads.
    expect(panel.getByText('60')).toBeTruthy()
    expect(panel.getByText('33.3%')).toBeTruthy()
    expect(panel.getByText('$0.001234')).toBeTruthy()
  }
})

test('the detail query is cached for the session across tabs', async () => {
  const urls = stubApi(detail())
  const { router } = await renderAt(`/ui/calls/${ID}`)
  await screen.findByRole('heading', { name: 'Who' })
  await router.navigate({ to: '/calls/$id', params: { id: ID }, search: { tab: 'usage' } })
  await screen.findByRole('heading', { name: 'Usage and cost' })
  await router.navigate({ to: '/calls/$id', params: { id: ID }, search: {} })
  await screen.findByRole('heading', { name: 'Who' })
  expect(urls.filter((u) => u.startsWith('/admin/calls/'))).toHaveLength(1)
})

test('tools rows link to the other call and show unmatched halves', async () => {
  const call = detail({
    tool_uses: [
      { source: 'client', tool_call_id: 't1', name: 'Read', arg_bytes: 100 },
      { source: 'client', tool_call_id: 't2', name: 'Bash', arg_bytes: 50 },
    ],
    tool_results: [{ source: 'client', tool_call_id: 't9', is_error: true, result_bytes: 10 }],
  })
  const urls = stubApi(call, [
    pair({}),
    pair({ tool_call_id: 't2', name: 'Bash', status: 'pending', result_call_id: null, duration_ms: null, duration_source: null, is_error: null, result_bytes: 0 }),
  ])
  await renderAt(`/ui/calls/${ID}?tab=%22tools%22`)
  await screen.findByText('2.0 KiB')
  const toolsUrl = urls.find((u) => u.startsWith('/admin/tool-calls'))!
  expect(toolsUrl).toContain('session=sess%2F1%3Aa')
  const rows = screen.getAllByRole('row').slice(1)
  expect(rows).toHaveLength(3)
  const [read, bash, orphan] = rows.map((r) => within(r))
  expect(read.getByText('2.0 KiB')).toBeTruthy()
  expect(read.getByText('300 ms (header)')).toBeTruthy()
  expect(read.getByText('Success')).toBeTruthy()
  expect(read.getByRole('link', { name: 'Resolving call' }).getAttribute('href')).toBe(`/ui/calls/${OTHER}`)
  expect(bash.getByText('Pending')).toBeTruthy()
  expect(bash.getByText('none yet')).toBeTruthy()
  expect(bash.queryByRole('link')).toBeNull()
  // A result whose use the pairing query did not return: name and use call unknown.
  expect(orphan.getByText('Error')).toBeTruthy()
  expect(orphan.getAllByText('unknown').length).toBeGreaterThanOrEqual(2)
  expect(orphan.queryByRole('link')).toBeNull()
  expect(screen.queryByText('Content is not stored', { selector: 'h2' })).toBeNull()
})

test('a result carried by this call links to the requesting call', () => {
  const call = detail({ tool_results: [{ source: 'client', tool_call_id: 't1', is_error: false, result_bytes: 2048 }] })
  const rows = buildToolRows(call, [pair({ use_call_id: OTHER, result_call_id: ID })])
  expect(rows).toHaveLength(1)
  expect(rows[0]).toMatchObject({ role: 'resolved', name: 'Read', otherCallId: OTHER, argBytes: 100, resultBytes: 2048 })
})

test('the pairing query covers uses timed at response end and results of uses a day older', async () => {
  const call = detail({
    ended_at: '2026-11-01T10:00:02.500Z',
    tool_uses: [{ source: 'client', tool_call_id: 't1', name: 'Read', arg_bytes: 100 }],
  })
  const urls = stubApi(call, [pair({})])
  await renderAt(`/ui/calls/${ID}?tab=%22tools%22`)
  await screen.findByText('2.0 KiB')
  const query = new URL(urls.find((u) => u.startsWith('/admin/tool-calls'))!, 'http://x').searchParams
  expect(query.get('from')).toBe('2026-10-31T10:00:00.000Z')
  expect(query.get('to')).toBe('2026-11-01T10:00:03.500Z')
})

test('the pairing query pages until every half of this call has its pair', async () => {
  const call = detail({
    tool_uses: [{ source: 'client', tool_call_id: 't1', name: 'Read', arg_bytes: 100 }],
    tool_results: [{ source: 'client', tool_call_id: 't0', is_error: false, result_bytes: 7 }],
  })
  const pages = [
    { tool_calls: [pair({}), pair({ tool_call_id: 'tx', use_call_id: OTHER, result_call_id: null })], next_cursor: 'c1' },
    { tool_calls: [pair({ tool_call_id: 't0', use_call_id: OTHER, result_call_id: ID })], next_cursor: 'c2' },
    { tool_calls: [], next_cursor: null },
  ]
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      urls.push(url)
      return new Response(JSON.stringify(pages[urls.length - 1]))
    }),
  )
  const out = await fetchToolPairs(call, { session: 'sess/1:a', limit: 500 })
  expect(urls).toHaveLength(2)
  expect(urls[1]).toContain('cursor=c1')
  expect(out.tool_calls.map((p) => p.tool_call_id)).toEqual(['t1', 't0'])
  const rows = buildToolRows(call, out.tool_calls)
  expect(rows[1]).toMatchObject({ role: 'resolved', name: 'Read', otherCallId: OTHER })
})

test('a call without a session skips the pairing query', async () => {
  const urls = stubApi(
    detail({ session_key: '', tool_uses: [{ source: 'client', tool_call_id: 't1', name: 'Read', arg_bytes: 1 }] }),
  )
  await renderAt(`/ui/calls/${ID}?tab=%22tools%22`)
  await screen.findByText('Read')
  expect(urls.some((u) => u.startsWith('/admin/tool-calls'))).toBe(false)
})

test('an API error shows a message', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify({ error: { type: 'not_found', message: 'no such call' } }), { status: 404 })),
  )
  await renderAt(`/ui/calls/${ID}`)
  await screen.findByRole('alert')
})

test('a failed pairing query on the overview shows an error with retry', async () => {
  const call = detail({ tool_uses: [{ source: 'client', tool_call_id: 't1', name: 'Read', arg_bytes: 100 }] })
  let failPairs = true
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/admin/calls/')) return new Response(JSON.stringify(call))
      if (failPairs) {
        return new Response(JSON.stringify({ error: { type: 'internal', message: 'pairing failed' } }), { status: 500 })
      }
      return new Response(JSON.stringify({ tool_calls: [pair({})], next_cursor: null }))
    }),
  )
  await renderAt(`/ui/calls/${ID}`)
  const alert = await screen.findByRole('alert', {}, { timeout: 5000 })
  failPairs = false
  fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(within(screen.getByRole('article', { name: 'Tool call Read' })).getByText('Success')).toBeTruthy(),
    { timeout: 5000 },
  )
  expect(screen.queryByRole('alert')).toBeNull()
})

test('the overview has no accessibility violations', async () => {
  stubApi(
    detail({
      context_compacted: true,
      tool_results: [{ source: 'client', tool_call_id: 't9', is_error: true, result_bytes: 10 }],
      tool_uses: Array.from({ length: 6 }, (_, i) => ({ source: 'client', tool_call_id: `t${i}`, name: 'Read', arg_bytes: 1 })),
    }),
  )
  const { container } = await renderAt(`/ui/calls/${ID}`)
  await screen.findByRole('heading', { name: 'Exchange' })
  const results = await axe.run(container)
  expect(results.violations).toEqual([])
})

test('detail page has no accessibility violations', async () => {
  stubApi(detail({ tool_uses: [{ source: 'client', tool_call_id: 't1', name: 'Read', arg_bytes: 1 }] }))
  const { container } = await renderAt(`/ui/calls/${ID}?tab=%22tools%22`)
  await screen.findByText('Read')
  const results = await axe.run(container)
  expect(results.violations).toEqual([])
})
