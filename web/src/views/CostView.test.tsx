import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory } from '@tanstack/react-router'
import axe from 'axe-core'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { App } from '../App'
import { createQueryClient } from '../queryClient'
import { createAppRouter } from '../router'
import type { PromptCost } from './costData'
import type { UsageBucket } from './dashboardData'
import { bucket } from './dashboardFixtures.test-util'

class StubEventSource {
  static CLOSED = 2
  readyState = 0
  onopen = null
  onerror = null
  addEventListener() {}
  close() {}
}

const NOW = '2026-11-01T12:00:00Z'

interface Fixture {
  overall: UsageBucket[]
  byGroup: Record<string, UsageBucket[]>
  prompts: PromptCost[]
  /** Every prompt of the range, which can be more than the table lists. */
  promptCount: number
  callsWithoutPrompt: number
  authMode: string
  /** The usage per group of the calls through the provider named in the query. */
  byProvider: Record<string, UsageBucket[]>
  /** Hold the usage answers until released, to see the loading state. */
  gate?: Promise<void>
}

const usage = { input_uncached: 60, cache_read: 40, cache_write_5m: 0, cache_write_1h: 0, output: 20, reasoning: 0 }

function prompt(over: Partial<PromptCost>): PromptCost {
  return {
    prompt_id: 'p',
    session_key: 's',
    first_ts: '2026-11-01T09:00:00Z',
    calls: 1,
    cost_micro_usd: 1,
    unpriced_calls: 0,
    tokens_in: 0,
    api_equivalent: false,
    ...over,
  }
}

const defaults: Fixture = {
  overall: [
    bucket({ start: '2026-11-01T10:00:00Z', calls: 30, cost_micro_usd: 3_000_000, usage, cache_hit_ratio: 0.4 }),
    bucket({ start: '2026-11-01T11:00:00Z', calls: 30, cost_micro_usd: 3_000_000, usage: { ...usage, cache_read: 140 }, cache_hit_ratio: 0.7 }),
  ],
  byGroup: {
    model: [
      bucket({ group: 'claude-opus', calls: 40, cost_micro_usd: 5_000_000 }),
      bucket({ group: 'claude-haiku', calls: 20, cost_micro_usd: 1_000_000 }),
      bucket({ group: 'mystery-model', calls: 4, unpriced_calls: 4, cost_micro_usd: 0 }),
    ],
    key: [
      bucket({ group: 'fm:task-a', calls: 10, cost_micro_usd: 4_000_000 }),
      bucket({ group: 'fm:task-b', calls: 10, cost_micro_usd: 1_000_000 }),
      bucket({ group: 'laptop', calls: 10, cost_micro_usd: 1_000_000 }),
    ],
    session: [
      bucket({ group: 's-big', cost_micro_usd: 4_000_000 }),
      bucket({ group: 's-small', cost_micro_usd: 100_000 }),
      bucket({ group: '', cost_micro_usd: 9_000_000 }),
    ],
    provider: [bucket({ group: 'anthropic', cost_micro_usd: 6_000_000 })],
  },
  prompts: [
    prompt({ prompt_id: 'prompt-aaaaaaaa-1', session_key: 's-big', calls: 2, cost_micro_usd: 1_000_000 }),
    prompt({ prompt_id: 'prompt-bbbbbbbb-2', session_key: 's-small', calls: 1, cost_micro_usd: null, unpriced_calls: 1 }),
  ],
  promptCount: 2,
  callsWithoutPrompt: 1,
  authMode: 'proxy_key',
  byProvider: {},
}

function stubApi(over: Partial<Fixture> = {}) {
  const f = { ...defaults, ...over, byGroup: { ...defaults.byGroup, ...over.byGroup } }
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      urls.push(url)
      const q = new URL(url, 'http://x').searchParams
      const json = (body: unknown) => new Response(JSON.stringify(body))
      if (url.startsWith('/admin/usage/prompts')) {
        return json({ prompts: f.prompts, prompt_count: f.promptCount, calls_without_prompt: f.callsWithoutPrompt })
      }
      if (url.startsWith('/admin/usage')) {
        await f.gate
        const group = q.get('group_by')
        if (q.get('provider') !== null) {
          return json({ group_by: group, grain: q.get('grain'), buckets: f.byProvider[group ?? ''] ?? [] })
        }
        return json({ group_by: group, grain: q.get('grain'), buckets: group ? (f.byGroup[group] ?? []) : f.overall })
      }
      if (url.startsWith('/admin/config')) {
        return json({
          providers: [{ name: 'anthropic', kind: 'anthropic', base_url: '', auth_mode: f.authMode, idle_timeout: '10m' }],
          pricing: [],
          keys: [],
        })
      }
      return new Response('{}', { status: 404 })
    }),
  )
  return urls
}

async function renderAt(path: string) {
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  const view = render(<App queryClient={queryClient} router={router} />)
  await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Cost' })).toBeTruthy())
  return { router, ...view }
}

const panel = (name: string) => screen.getByRole('region', { name })
const kpi = (label: string) => screen.getByText(label, { selector: 'dt' }).parentElement as HTMLElement

beforeEach(() => {
  vi.stubGlobal('EventSource', StubEventSource)
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(NOW))
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

test('/ui/cost shows totals and is current in the sidebar', async () => {
  stubApi()
  await renderAt('/ui/cost')
  await waitFor(() => expect(kpi('Cost').textContent).toContain('$6.00'))
  expect(kpi('Calls').textContent).toContain('60')
  // 40 of 100 and 140 of 200 input tokens read from cache: 180 of 300.
  expect(kpi('Cache hit').textContent).toContain('60.0%')
  const nav = screen.getByRole('navigation', { name: 'Main' })
  expect(within(nav).getByRole('link', { name: 'Cost' }).getAttribute('aria-current')).toBe('page')
})

test('the panels show a loading state while a long range loads', async () => {
  let release!: () => void
  const gate = new Promise<void>((resolve) => (release = resolve))
  stubApi({ gate })
  await renderAt('/ui/cost?range=30d')
  expect(within(panel('Cost over time by model')).getByRole('status').textContent).toContain('a long range can take a few seconds')
  expect(within(panel('Cost by model')).getByRole('status')).toBeTruthy()
  release()
  await within(panel('Cost by model')).findByRole('table', { name: 'Cost by model' })
  expect(within(panel('Cost by model')).queryByRole('status')).toBeNull()
})

test('cost by model lists every model with its share, and unknown cost is not zero', async () => {
  stubApi()
  await renderAt('/ui/cost')
  const table = await within(panel('Cost by model')).findByRole('table', { name: 'Cost by model' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows.map((r) => r.querySelector('th')?.textContent)).toEqual(['claude-opus', 'claude-haiku', 'mystery-model'])
  expect(rows[0].textContent).toContain('$5.00')
  expect(rows[0].textContent).toContain('83%')
  // No price matched: unknown, with no share, and never $0.00.
  expect(rows[2].textContent).toContain('unknown')
  expect(rows[2].textContent).not.toContain('$0.00')
})

test('the group-by control is in the URL and re-queries by that group', async () => {
  const urls = stubApi()
  const { router } = await renderAt('/ui/cost')
  fireEvent.click(screen.getByRole('button', { name: 'key' }))
  await within(await screen.findByRole('region', { name: 'Cost by key' })).findByRole('table', { name: 'Cost by key' })
  expect(router.state.location.search).toMatchObject({ group_by: 'key' })
  expect(urls.some((u) => u.startsWith('/admin/usage') && u.includes('group_by=key') && u.includes('grain=hour'))).toBe(true)
  // The default grouping stays out of the URL.
  fireEvent.click(screen.getByRole('button', { name: 'model' }))
  await waitFor(() => expect(router.state.location.search).not.toHaveProperty('group_by'))
})

test('cost per firstmate task lists the fm: keys by task id', async () => {
  stubApi()
  await renderAt('/ui/cost')
  const table = await within(panel('Cost per firstmate task')).findByRole('table', { name: 'Cost per firstmate task' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows.map((r) => r.querySelector('th')?.textContent)).toEqual(['task-a', 'task-b'])
  expect(rows[0].textContent).toContain('$4.00')
  expect(rows[0].textContent).toContain('80%')
  // The link filters the call log by the full key name.
  expect(within(rows[0]).getByRole('link').getAttribute('href')).toContain('fm%3Atask-a')
})

test('without fm: keys the task panel says so', async () => {
  stubApi({ byGroup: { key: [bucket({ group: 'laptop' })] } })
  await renderAt('/ui/cost')
  await within(panel('Cost per firstmate task')).findByText(/No fm:<task-id> keys made calls/)
})

test('top sessions rank by cost and leave out calls without a session', async () => {
  stubApi()
  await renderAt('/ui/cost')
  const table = await within(panel('Top sessions by cost')).findByRole('table', { name: 'Cost by session' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows.map((r) => r.querySelector('th')?.textContent)).toEqual(['s-big', 's-small'])
  expect(within(rows[0]).getByRole('link', { name: 's-big' })).toBeTruthy()
})

test('cost per prompt lists the prompts of the server; an unpriced prompt is unknown', async () => {
  stubApi()
  await renderAt('/ui/cost')
  const table = await within(panel('Cost per prompt')).findByRole('table', { name: 'Cost per prompt' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows).toHaveLength(2)
  expect(rows[0].textContent).toContain('$1.00')
  expect(rows[0].textContent).toContain('2')
  expect(rows[1].textContent).toContain('unknown')
  expect(within(panel('Cost per prompt')).getByText(/1 calls have no prompt id/)).toBeTruthy()
})

test('cost per prompt asks the server for the whole range and says when it lists only the most costly prompts', async () => {
  const urls = stubApi({ promptCount: 1_030 })
  await renderAt('/ui/cost')
  await within(panel('Cost per prompt')).findByText('Showing the 2 most costly of 1,030 prompts; 1,028 more are not shown.')
  expect(urls.some((u) => u.startsWith('/admin/usage/prompts') && u.includes('limit=25'))).toBe(true)
  expect(urls.some((u) => u.startsWith('/admin/calls'))).toBe(false)
})

test('cost per prompt claims no limit when the table lists every prompt', async () => {
  stubApi()
  await renderAt('/ui/cost')
  await within(panel('Cost per prompt')).findByRole('table')
  expect(within(panel('Cost per prompt')).queryByText(/most costly/)).toBeNull()
})

test('the cache hit ratio has a chart and a table fallback', async () => {
  stubApi()
  await renderAt('/ui/cost')
  const cache = panel('Cache hit ratio over time')
  const chart = await within(cache).findByRole('img', { name: /Cache hit ratio per hour: 2 of 24 intervals have a value, from 40.0% to 70.0%/ })
  expect(chart).toBeTruthy()
  const details = cache.querySelector('details') as HTMLDetailsElement
  const table = within(details).getByRole('table', { name: 'Cache hit ratio per hour', hidden: true })
  const values = within(table).getAllByRole('row', { hidden: true }).slice(1).map((r) => r.textContent)
  expect(values).toHaveLength(2)
  expect(values[0]).toContain('40.0%')
  expect(values[1]).toContain('70.0%')
})

test('the cache table lists an interval with a 0% ratio', async () => {
  const none = { ...usage, cache_read: 0 }
  stubApi({
    overall: [
      bucket({ start: '2026-11-01T09:00:00Z', calls: 5, cost_micro_usd: 100, usage: none, cache_hit_ratio: 0 }),
      bucket({ start: '2026-11-01T10:00:00Z', calls: 5, cost_micro_usd: 100, usage, cache_hit_ratio: 0.4 }),
      bucket({ start: '2026-11-01T11:00:00Z', calls: 5, cost_micro_usd: 100, usage: none, cache_hit_ratio: 0 }),
    ],
  })
  await renderAt('/ui/cost')
  const cache = panel('Cache hit ratio over time')
  await within(cache).findByRole('img', { name: /Cache hit ratio per hour/ })
  const table = within(cache).getByRole('table', { name: 'Cache hit ratio per hour', hidden: true })
  const values = within(table).getAllByRole('row', { hidden: true }).slice(1).map((r) => r.querySelector('td')?.textContent)
  expect(values).toEqual(['0.0%', '40.0%', '0.0%'])
})

test('the cost chart has a named legend, a label with the groups and a table fallback', async () => {
  stubApi()
  await renderAt('/ui/cost')
  const costPanel = panel('Cost over time by model')
  const chart = await within(costPanel).findByRole('img', { name: /Cost per hour by model/ })
  expect(chart.getAttribute('aria-label')).toContain('models claude-opus, claude-haiku')
  // The errors overlay belongs to the overview, not to this chart.
  expect(within(costPanel).queryByText(/errors \(right scale\)/)).toBeNull()
  const legend = within(costPanel).getAllByRole('listitem').map((li) => li.textContent)
  expect(legend).toContain('claude-opus')
  expect(within(costPanel).getByText('Show as table')).toBeTruthy()
})

test('when no price matched any call the chart says cost is unknown', async () => {
  stubApi({ overall: [bucket({ calls: 5, unpriced_calls: 5, cost_micro_usd: 0 })] })
  await renderAt('/ui/cost')
  await within(panel('Cost over time by model')).findByText(/Cost is unknown: no price matched/)
  expect(kpi('Cost').querySelector('.kpi-value')?.textContent).toBe('unknown')
})

test('an empty range says so instead of drawing empty charts', async () => {
  stubApi({ overall: [], byGroup: { model: [], key: [], session: [] }, prompts: [], promptCount: 0, callsWithoutPrompt: 0 })
  await renderAt('/ui/cost')
  await within(panel('Cost over time by model')).findByText(/No calls in the last 24h/)
  await within(panel('Cache hit ratio over time')).findByText(/no cache hit ratio/)
  await within(panel('Cost per prompt')).findByText(/No calls with a prompt id/)
})

test('client_passthrough cost carries the API-equivalent label', async () => {
  const urls = stubApi({ authMode: 'client_passthrough' })
  await renderAt('/ui/cost')
  await waitFor(() => expect(kpi('Cost').textContent).toContain('API-equivalent'))
  expect(urls.some((u) => u.includes('group_by=provider'))).toBe(true)
  await screen.findByText(/Cost includes the API-equivalent cost/)
})

test('without a passthrough provider nothing is API-equivalent', async () => {
  const urls = stubApi()
  await renderAt('/ui/cost')
  await waitFor(() => expect(kpi('Cost').textContent).toContain('$6.00'))
  await within(panel('Cost per prompt')).findByRole('table')
  expect(screen.queryByText(/API-equivalent/)).toBeNull()
  expect(urls.some((u) => u.includes('group_by=provider'))).toBe(false)
})

test('a passthrough prompt is labelled API-equivalent', async () => {
  stubApi({
    authMode: 'client_passthrough',
    prompts: [prompt({ prompt_id: 'p-plan', session_key: 's', cost_micro_usd: 250_000, api_equivalent: true })],
  })
  await renderAt('/ui/cost')
  const table = await within(panel('Cost per prompt')).findByRole('table')
  await waitFor(() => expect(within(table).getByText(/API-equivalent/)).toBeTruthy())
})

test('a group with calls through a passthrough provider is labelled API-equivalent', async () => {
  const urls = stubApi({
    authMode: 'client_passthrough',
    byProvider: {
      model: [bucket({ group: 'claude-haiku', cost_micro_usd: 1_000_000 })],
      session: [bucket({ group: 's-small', cost_micro_usd: 100_000 })],
      key: [bucket({ group: 'fm:task-b', cost_micro_usd: 1_000_000 })],
    },
  })
  await renderAt('/ui/cost')
  const label = (table: HTMLElement, name: string) =>
    within(table).getAllByRole('row').find((r) => r.querySelector('th')?.textContent === name)?.textContent ?? ''
  const models = await within(panel('Cost by model')).findByRole('table', { name: 'Cost by model' })
  await waitFor(() => expect(label(models, 'claude-haiku')).toContain('API-equivalent'))
  expect(label(models, 'claude-opus')).not.toContain('API-equivalent')
  const sessions = within(panel('Top sessions by cost')).getByRole('table', { name: 'Cost by session' })
  await waitFor(() => expect(label(sessions, 's-small')).toContain('API-equivalent'))
  expect(label(sessions, 's-big')).not.toContain('API-equivalent')
  const tasks = within(panel('Cost per firstmate task')).getByRole('table', { name: 'Cost per firstmate task' })
  await waitFor(() => expect(label(tasks, 'task-b')).toContain('API-equivalent'))
  expect(label(tasks, 'task-a')).not.toContain('API-equivalent')
  expect(urls.some((u) => u.includes('group_by=session') && u.includes('provider=anthropic'))).toBe(true)
})

test('filters in the URL reach the API and can be cleared', async () => {
  const urls = stubApi()
  await renderAt('/ui/cost?key=fm%3Atask-a&range=7d')
  await waitFor(() => expect(urls.some((u) => u.startsWith('/admin/usage') && u.includes('key=fm%3Atask-a'))).toBe(true))
  expect(urls.some((u) => u.startsWith('/admin/usage/prompts') && u.includes('key=fm%3Atask-a'))).toBe(true)
  expect(screen.getByLabelText('Active filters').textContent).toContain('key = fm:task-a')
  expect(screen.getByRole('link', { name: 'Clear filters' })).toBeTruthy()
})

test('an API error shows in the panel with a retry', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: { code: 'internal', message: 'boom' } }), { status: 400 })))
  await renderAt('/ui/cost')
  await waitFor(() => expect(screen.getAllByRole('alert').length).toBeGreaterThan(0))
  expect(screen.getAllByRole('button', { name: 'Retry' }).length).toBeGreaterThan(0)
})

test('the page has no accessibility violations', async () => {
  stubApi()
  const { container } = await renderAt('/ui/cost')
  await within(panel('Cost per prompt')).findByRole('table')
  await within(panel('Cache hit ratio over time')).findByRole('img')
  vi.useRealTimers()
  const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } })
  expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([])
})
