import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory } from '@tanstack/react-router'
import axe from 'axe-core'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { App } from '../App'
import { createQueryClient } from '../queryClient'
import { createAppRouter } from '../router'
import { at, bigSession, detail, parallelSession, sessionCall } from './sessionFixtures.test-util'
import { buildSessionModel, defaultExpanded, flattenTree } from './sessionTree'
import type { SessionDetail, ToolCall } from './sessionTree'
import type { SessionSummary } from './SessionList'
import { cacheHitRatio, sessionDurationMs } from './SessionList'

type Routes = Record<string, (url: URL) => unknown>

/** Answers each admin path prefix with a JSON body; anything else is a 404. */
function stubApi(routes: Routes) {
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (raw: string) => {
      urls.push(raw)
      const url = new URL(raw, 'http://x')
      const match = Object.keys(routes)
        .sort((a, b) => b.length - a.length)
        .find((p) => url.pathname.startsWith(p))
      if (!match) {
        return new Response(JSON.stringify({ error: { type: 'not_found', message: 'no' } }), { status: 404 })
      }
      const body = routes[match](url)
      if (body instanceof Response) return body
      return new Response(JSON.stringify(body))
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
const treeItems = () => within(screen.getByRole('tree', { name: 'Session tree' })).getAllByRole('treeitem')
const toolList = (tools: ToolCall[]) => ({ tool_calls: tools, next_cursor: null })

function summary(key: string, over: Partial<SessionSummary> = {}): SessionSummary {
  return {
    session_key: key,
    key: 'ci',
    client: 'claude-code',
    first_ts: at(0),
    last_ts: at(90_000),
    ended_at: at(95_000),
    calls: 12,
    prompts: 4,
    tool_calls: 7,
    errors: 0,
    agent_count: 2,
    models: ['m'],
    usage: { input_uncached: 25, cache_read: 75, cache_write_5m: 0, cache_write_1h: 0, output: 5, reasoning: 0 },
    cost_micro_usd: 2_500_000,
    unpriced_calls: 0,
    llm_ms: 42_000,
    tool_ms: 28_000,
    tool_ms_estimated: false,
    ...over,
  }
}

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(480)
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1200)
  // jsdom has no layout: give measured tree rows a one-line height.
  vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(28)
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    width: 1200, height: 480, top: 0, left: 0, right: 1200, bottom: 480, x: 0, y: 0, toJSON: () => ({}),
  })
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

// Session list.

test('session list shows the columns', async () => {
  const urls = stubApi({
    '/admin/sessions': () => ({
      sessions: [summary('team/a:1', { errors: 3 }), summary('s2', { cost_micro_usd: null, usage: { input_uncached: 0, cache_read: 0, cache_write_5m: 0, cache_write_1h: 0, output: 0, reasoning: 0 } })],
      next_cursor: null,
    }),
    ...routesFor(parallelSession().detail),
  })
  const { container, router } = await renderAt('/ui/sessions?range=7d&client=claude-code')
  const table = await screen.findByRole('table', { name: 'Sessions' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows).toHaveLength(2)
  const headers = within(table).getAllByRole('columnheader').map((h) => h.textContent?.replace(/[▲▼]/g, '').trim())
  expect(headers).toEqual(['Start', 'Session', 'Duration', 'Model / tool time', 'Client', 'Key', 'Prompts', 'Calls', 'Tool calls', 'Tools per call', 'Cost', 'Cache hit', 'Errors'])
  const first = within(rows[0])
  expect(first.getByText('team/a:1')).toBeTruthy()
  // Duration runs from the first call start to the latest call end.
  expect(first.getByText('1 min 35 s')).toBeTruthy()
  expect(first.getByText('4')).toBeTruthy()
  expect(first.getByText('12')).toBeTruthy()
  expect(first.getByText('7')).toBeTruthy()
  // 7 tool calls over 12 calls.
  expect(first.getByText('0.6')).toBeTruthy()
  expect(first.getByText('$2.50')).toBeTruthy()
  expect(first.getByText('LLM 42.00 s (60%) · tools 28.00 s (40%)')).toBeTruthy()
  expect(first.getByText('75.0%')).toBeTruthy()
  expect(first.getByText('3')).toBeTruthy()
  // Only the unknown cost and cache hit of the second row read unknown.
  expect(first.queryAllByText('unknown')).toHaveLength(0)
  expect(within(rows[1]).getAllByText('unknown')).toHaveLength(2)

  const req = new URL(urls.find((u) => u.startsWith('/admin/sessions'))!, 'http://x')
  expect(req.searchParams.getAll('client')).toEqual(['claude-code'])
  expect(req.searchParams.get('limit')).toBe('100')
  expect(Date.parse(req.searchParams.get('to')!) - Date.parse(req.searchParams.get('from')!)).toBe(7 * 86_400_000)
  expect((await axe.run(container)).violations.filter((v) => v.impact === 'critical' || v.impact === 'serious')).toEqual([])

  // Keyboard: j moves to the next row, Enter opens it.
  rows[0].focus()
  fireEvent.keyDown(rows[0], { key: 'j' })
  expect(document.activeElement).toBe(rows[1])
  expect(rows[1].tabIndex).toBe(0)
  expect(rows[0].tabIndex).toBe(-1)
  fireEvent.keyDown(rows[1], { key: 'k' })
  expect(document.activeElement).toBe(rows[0])
  fireEvent.keyDown(rows[0], { key: 'Enter' })
  await waitFor(() => expect(router.state.location.href).toBe('/sessions/team%2Fa%3A1'))
  await screen.findByRole('tree', { name: 'Session tree' })
})

test('session list helpers: cache hit and duration', () => {
  expect(cacheHitRatio(summary('x').usage)).toBe(0.75)
  expect(sessionDurationMs({ first_ts: at(0), ended_at: at(5000) })).toBe(5000)
  expect(sessionDurationMs({ first_ts: at(5000), ended_at: at(0) })).toBeNull()
})

// Session view.

function routesFor(d: SessionDetail, tools: ToolCall[] = []): Routes {
  return {
    '/admin/sessions/': () => d,
    '/admin/tool-calls': () => toolList(tools),
  }
}

test('deep link with an encoded key loads the session and its tool calls, in tree mode by default', async () => {
  const { detail: d, tools } = parallelSession()
  d.summary = { ...d.summary, llm_ms: 42_000, tool_ms: 28_000 }
  const urls = stubApi(routesFor(d, tools))
  const { container } = await renderAt('/ui/sessions/team%2Fa%3A1')
  await screen.findByRole('tree', { name: 'Session tree' })
  expect(urls).toContain('/admin/sessions/team%2Fa%3A1')
  await waitFor(() => expect(urls.some((u) => u.startsWith('/admin/tool-calls'))).toBe(true))
  const toolReq = new URL(urls.find((u) => u.startsWith('/admin/tool-calls'))!, 'http://x')
  expect(toolReq.searchParams.get('session')).toBe('team/a:1')
  expect(toolReq.searchParams.get('from')).toBe(at(0))
  expect(Date.parse(toolReq.searchParams.get('to')!)).toBeGreaterThan(Date.parse(at(900)))
  expect(screen.getByText('team/a:1')).toBeTruthy()

  await waitFor(() => expect(treeItems().map((r) => r.getAttribute('aria-label'))).toContain('Tool Bash'))
  const labels = treeItems().map((r) => [r.getAttribute('aria-label'), r.getAttribute('aria-level')])
  expect(labels.slice(0, 4)).toEqual([
    ['Prompt Prompt 1', '1'],
    ['Agent main agent', '2'],
    ['LLM m', '3'],
    ['Sub-agent Explore', '3'],
  ])
  // Every row shows duration, tokens, cost and errors; the prompt holds two errors.
  const prompt = treeItems()[0]
  expect(within(prompt).getByText('900 ms')).toBeTruthy()
  expect(within(prompt).getByText('60')).toBeTruthy()
  expect(within(prompt).getByText('$0.000060')).toBeTruthy()
  expect(within(prompt).getByText('2')).toBeTruthy()
  expect(within(prompt).getByText('6 LLM calls · 2 tool calls · 0.3 tools per call')).toBeTruthy()
  expect(within(treeItems()[1]).getByText('6 LLM calls · 2 tool calls · 0.3 tools per call')).toBeTruthy()
  // Model and tool time of the prompt are busy time: the two parallel tool calls add up.
  expect(within(prompt).getByText('LLM 1.00 s (85%) · tools 170 ms (15%)')).toBeTruthy()
  // The header shows the session total from the summary, with the note on parallel time.
  expect(screen.getByText('LLM 42.00 s (60%) · tools 28.00 s (40%)')).toBeTruthy()
  expect(screen.getAllByText(/Parallel tool calls and sub-agents overlap in wall time/).length).toBeGreaterThan(0)
  expect(screen.getByRole('link', { name: 'Tree' }).getAttribute('aria-current')).toBe('page')
  expect(screen.getByRole('link', { name: 'Time' }).getAttribute('aria-current')).toBeNull()
  expect((await axe.run(container)).violations.filter((v) => v.impact === 'critical' || v.impact === 'serious')).toEqual([])
})

test('mode is in the URL: ?mode=time opens the waterfall, the toggle switches back, a bad mode degrades to tree', async () => {
  const { detail: d, tools } = parallelSession()
  stubApi(routesFor(d, tools))
  const { router } = await renderAt('/ui/sessions/s1?mode=time')
  const waterfall = await screen.findByRole('list', { name: 'Session waterfall' })
  const lanes = within(waterfall).getAllByRole('listitem')
  expect(lanes.filter((l) => !l.getAttribute('aria-label')?.endsWith(', more')).map((l) => l.getAttribute('aria-label'))).toEqual([
    'main agent',
    'Explore',
    'Plan',
  ])
  // Bars link to the call detail view.
  const failed = within(waterfall).getByRole('link', { name: /^LLM m, 100 ms, error$/ })
  expect(failed.getAttribute('href')).toBe('/ui/calls/a2')
  expect(screen.getByRole('link', { name: 'Time' }).getAttribute('aria-current')).toBe('page')
  // Only the current mode is marked active, even though both links share the path.
  expect(screen.getByRole('link', { name: 'Tree' }).getAttribute('aria-current')).toBeNull()
  expect(screen.getByRole('link', { name: 'Tree' }).classList.contains('active')).toBe(false)

  fireEvent.click(screen.getByRole('link', { name: 'Tree' }))
  await screen.findByRole('tree', { name: 'Session tree' })
  expect(search(router)).toEqual({})

  await router.navigate({ to: '/sessions/$key', params: { key: 's1' }, search: { mode: 'bogus' } as never })
  await screen.findByRole('tree', { name: 'Session tree' })
  expect(search(router).mode).toBeUndefined()
})

test('tree keyboard: arrows move, left and right collapse and expand, Enter opens the call detail', async () => {
  const { detail: d, tools } = parallelSession()
  stubApi(routesFor(d, tools))
  const { router } = await renderAt('/ui/sessions/s1')
  await waitFor(() => expect(treeItems().length).toBe(12))
  const [prompt] = treeItems()
  expect(prompt.tabIndex).toBe(0)
  prompt.focus()
  fireEvent.keyDown(prompt, { key: 'ArrowLeft' })
  await waitFor(() => expect(treeItems()).toHaveLength(1))
  expect(treeItems()[0].getAttribute('aria-expanded')).toBe('false')
  fireEvent.keyDown(treeItems()[0], { key: 'ArrowRight' })
  await waitFor(() => expect(treeItems()).toHaveLength(12))

  fireEvent.keyDown(treeItems()[0], { key: 'ArrowDown' })
  expect(document.activeElement).toBe(treeItems()[1])
  fireEvent.keyDown(treeItems()[1], { key: 'j' })
  const call = treeItems()[2]
  expect(document.activeElement).toBe(call)
  expect(call.getAttribute('aria-label')).toBe('LLM m')
  // Left on a leaf goes to its parent.
  fireEvent.keyDown(call, { key: 'ArrowLeft' })
  expect(document.activeElement).toBe(treeItems()[1])
  fireEvent.keyDown(treeItems()[1], { key: 'ArrowDown' })
  fireEvent.keyDown(treeItems()[2], { key: 'Enter' })
  await waitFor(() => expect(router.state.location.pathname).toBe('/calls/m1'))
})

test('a click on a tool row opens the call that requested it', async () => {
  const { detail: d, tools } = parallelSession()
  stubApi(routesFor(d, tools))
  const { router } = await renderAt('/ui/sessions/s1')
  await waitFor(() => expect(treeItems().some((r) => r.getAttribute('aria-label') === 'Tool Bash')).toBe(true))
  const tool = treeItems().find((r) => r.getAttribute('aria-label') === 'Tool Bash')!
  // The error badge of the failed tool shows on its row.
  expect(treeItems().some((r) => within(r).queryByText('error'))).toBe(true)
  fireEvent.click(tool)
  await waitFor(() => expect(router.state.location.pathname).toBe('/calls/a1'))
})

test('older prompts are collapsed by default; compaction and idle gaps are marked', async () => {
  const calls = Array.from({ length: 5 }, (_, i) =>
    sessionCall(`c${i}`, { offset: i * 3_600_000, prompt_id: `p${i}`, context_compacted: i === 4 }),
  )
  stubApi(routesFor(detail(calls)))
  await renderAt('/ui/sessions/s1')
  await waitFor(() => expect(treeItems().length).toBeGreaterThan(0))
  const expanded = treeItems()
    .filter((r) => r.getAttribute('aria-level') === '1')
    .map((r) => r.getAttribute('aria-expanded'))
  expect(expanded).toEqual(['false', 'false', 'true', 'true', 'true'])
  expect(screen.getByText('compacted')).toBeTruthy()
  expect(screen.getByText(/idle gap \(4\) collapsed/)).toBeTruthy()
  expect(screen.getByText(/context compacted \(1\)/)).toBeTruthy()
  // A collapsed prompt opens on its own toggle.
  fireEvent.click(treeItems()[0])
  await waitFor(() => expect(treeItems()[0].getAttribute('aria-expanded')).toBe('true'))
})

test('tool calls are paged through with the cursor', async () => {
  const { detail: d, tools } = parallelSession()
  const urls = stubApi({
    '/admin/sessions/': () => d,
    '/admin/tool-calls': (url) =>
      url.searchParams.get('cursor') === 'next'
        ? { tool_calls: [tools[1]], next_cursor: null }
        : { tool_calls: [tools[0]], next_cursor: 'next' },
  })
  await renderAt('/ui/sessions/s1')
  await waitFor(() => expect(treeItems().filter((r) => r.getAttribute('aria-label') === 'Tool Bash')).toHaveLength(2))
  expect(urls.filter((u) => u.startsWith('/admin/tool-calls'))).toHaveLength(2)
  expect(screen.getByText('Tool calls').nextElementSibling?.textContent).toBe('2')
})

test('an unknown session shows the not-found message', async () => {
  stubApi({})
  await renderAt('/ui/sessions/missing')
  expect(await screen.findByText(/Not found/)).toBeTruthy()
})

test('a failed tool-call query keeps the tree and offers a retry', async () => {
  const { detail: d } = parallelSession()
  stubApi({
    '/admin/sessions/': () => d,
    '/admin/tool-calls': () => new Response(JSON.stringify({ error: { type: 'invalid_parameter', message: 'x', parameter: 'from' } }), { status: 400 }),
  })
  await renderAt('/ui/sessions/s1')
  expect(await screen.findByText('Tool calls could not be loaded.')).toBeTruthy()
  expect(treeItems().length).toBeGreaterThan(0)
})

test('1,000-call session renders a bounded number of rows in both modes', async () => {
  const { detail: d, tools } = bigSession(1000)
  stubApi(routesFor(d, tools))
  const startedAt = performance.now()
  const { router } = await renderAt('/ui/sessions/s1')
  await waitFor(() => expect(treeItems().some((r) => r.getAttribute('aria-label') === 'Tool Bash')).toBe(true))
  const elapsed = performance.now() - startedAt
  // Only the visible rows and the overscan are in the DOM.
  expect(treeItems().length).toBeLessThan(60)
  expect(elapsed).toBeLessThan(5000)
  // The latest prompts are open by default: hundreds of rows are in the list, not the DOM.
  const model = buildSessionModel(d, tools)
  const listed = flattenTree(model.prompts, (n) => defaultExpanded(n, model.prompts.length)).length
  expect(listed).toBeGreaterThan(400)
  expect(screen.getByRole('tree').style.height).toBe(`${listed * 28}px`)

  await router.navigate({ to: '/sessions/$key', params: { key: 's1' }, search: { mode: 'time' } })
  const waterfall = await screen.findByRole('list', { name: 'Session waterfall' })
  expect(within(waterfall).getAllByRole('listitem').length).toBeLessThan(60)
})

test('a session cell in the call log opens the session view', async () => {
  const { detail: d, tools } = parallelSession()
  stubApi({
    ...routesFor(d, tools),
    '/admin/calls': () => ({ calls: [sessionCall('m1', { session_key: 'team/a:1' })], next_cursor: null, count: 1, count_capped: false }),
    '/admin/usage': () => ({ group_by: null, grain: 'hour', buckets: [] }),
  })
  const { router } = await renderAt('/ui/calls')
  fireEvent.click(await screen.findByRole('link', { name: 'team/a:1' }))
  await screen.findByRole('tree', { name: 'Session tree' })
  expect(router.state.location.href).toBe('/sessions/team%2Fa%3A1')
})
