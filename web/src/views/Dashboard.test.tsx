import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory } from '@tanstack/react-router'
import axe from 'axe-core'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { App } from '../App'
import { createQueryClient } from '../queryClient'
import { createAppRouter } from '../router'
import { call } from './callLogRows.test'
import type { CallSummary } from './callLogRows'
import { bucket } from './dashboardFixtures.test-util'
import type { UsageBucket } from './dashboardData'
import type { MonthRow, ProjectsResponse, PunchcardCell, SessionCostRow, ToolCostRow } from './costOverviewData'

class FakeEventSource {
  static CLOSED = 2
  static instances: FakeEventSource[] = []
  readyState = 0
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  private listeners = new Map<string, ((e: MessageEvent<string>) => void)[]>()
  constructor(public url: string) {
    FakeEventSource.instances.push(this)
  }
  addEventListener(type: string, fn: (e: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn])
  }
  close() {}
  open() {
    act(() => this.onopen?.())
  }
  finish(c: CallSummary) {
    act(() => {
      for (const fn of this.listeners.get('call.finished') ?? []) {
        fn(new MessageEvent('call.finished', { data: JSON.stringify(c), lastEventId: c.id }))
      }
    })
  }
}

const NOW = '2026-11-01T12:00:00Z'
// The default 24h window and the 24h before it.
const CURRENT_FROM = '2026-10-31T12:00:00.000Z'

interface Fixture {
  current: UsageBucket[]
  previous: UsageBucket[]
  byGroup: Record<string, UsageBucket[]>
  calls: CallSummary[]
  sessions: object[]
  authMode: string
  months: MonthRow[]
  projects: ProjectsResponse
  cells: PunchcardCell[]
  costSessions: SessionCostRow[]
  sessionCount: number
  tools: ToolCostRow[]
  toolCount: number
  /** Cost-section paths that answer with an error. */
  failing: string[]
  /** The headline usage query never answers, so the tiles do not render. */
  holdHeadline: boolean
}

const month = (m: string, over: Partial<MonthRow> = {}): MonthRow => {
  const [y, mo] = m.split('-').map(Number)
  return {
    month: m,
    start: new Date(Date.UTC(y, mo - 1, 1)).toISOString(),
    end: new Date(Date.UTC(y, mo, 1)).toISOString(),
    calls: 10,
    cost_micro_usd: 1_000_000,
    unpriced_calls: 0,
    api_equivalent: false,
    partial: false,
    ...over,
  }
}
const projectRow = (project: string, cost: number | null, over: object = {}) => ({
  project,
  calls: 5,
  sessions: 2,
  cost_micro_usd: cost,
  unpriced_calls: 0,
  api_equivalent: false,
  ...over,
})

const cell = (weekday: number, hour: number, cost: number | null, over: Partial<PunchcardCell> = {}): PunchcardCell => ({
  weekday,
  hour,
  calls: 10,
  cost_micro_usd: cost,
  unpriced_calls: cost === null ? 10 : 0,
  api_equivalent: false,
  ...over,
})
const costSession = (key: string, cost: number | null, over: Partial<SessionCostRow> = {}): SessionCostRow => ({
  session_key: key,
  key: 'k',
  client: 'claude-code',
  first_ts: '2026-11-01T10:00:00Z',
  last_ts: '2026-11-01T10:30:00Z',
  ended_at: '2026-11-01T10:31:00Z',
  calls: 12,
  prompts: 2,
  tool_calls: 4,
  errors: 0,
  agent_count: 1,
  models: ['claude-opus'],
  usage: { input_uncached: 1, cache_read: 0, cache_write_5m: 0, cache_write_1h: 0, output: 1, reasoning: 0 },
  cost_micro_usd: cost,
  unpriced_calls: cost === null ? 12 : 0,
  llm_ms: 1000,
  tool_ms: 500,
  tool_ms_estimated: false,
  api_equivalent: false,
  ...over,
})
const toolCost = (tool: string, cost: number | null, over: Partial<ToolCostRow> = {}): ToolCostRow => ({
  tool,
  uses: 8,
  errors: 0,
  result_bytes: 1000,
  cost_micro_usd: cost,
  unpriced_calls: cost === null ? 4 : 0,
  api_equivalent: false,
  ...over,
})

const defaults: Fixture = {
  current: [
    bucket({ start: '2026-11-01T10:00:00Z', calls: 30, errors: 3, cost_micro_usd: 3_000_000 }),
    bucket({ start: '2026-11-01T11:00:00Z', calls: 30, errors: 0, cost_micro_usd: 3_000_000 }),
  ],
  previous: [bucket({ start: '2026-10-31T10:00:00Z', calls: 40, errors: 0, cost_micro_usd: 4_000_000 })],
  byGroup: {
    model: [
      bucket({ group: 'claude-opus', calls: 40, errors: 3, cost_micro_usd: 5_000_000 }),
      bucket({ group: 'claude-haiku', calls: 20, errors: 0, cost_micro_usd: 1_000_000 }),
    ],
    error_class: [
      bucket({ group: '', calls: 55, errors: 0 }),
      bucket({ group: 'rate_limited', calls: 2, errors: 2 }),
      bucket({ group: 'upstream_5xx', calls: 1, errors: 1 }),
    ],
    provider: [bucket({ group: 'anthropic', cost_micro_usd: 6_000_000 })],
  },
  authMode: 'proxy_key',
  // September $3, October $6, November so far $1: October is the most costly month, $6 of $10.
  months: [
    month('2026-09', { calls: 20, cost_micro_usd: 3_000_000 }),
    month('2026-10', { calls: 30, cost_micro_usd: 6_000_000, unpriced_calls: 3 }),
    month('2026-11', { calls: 4, cost_micro_usd: 1_000_000, partial: true }),
  ],
  projects: {
    projects: [projectRow('spyglass', 5_000_000), projectRow('docs-site', 1_500_000, { unpriced_calls: 2 })],
    project_count: 2,
    unassigned: { calls: 7, cost_micro_usd: 3_500_000, unpriced_calls: 0 },
  },
  // Tuesday 14:00 is the most costly hour, $2 of the $3.50 known; Saturday 22:00 has calls and no price.
  cells: [cell(1, 9, 500_000), cell(2, 14, 2_000_000), cell(6, 22, null), cell(7, 0, 1_000_000)],
  costSessions: [costSession('s-big', 4_000_000), costSession('s-small', 1_500_000, { errors: 3, unpriced_calls: 2 })],
  sessionCount: 5,
  tools: [toolCost('Bash', 3_000_000, { uses: 20, errors: 2 }), toolCost('Read', 1_000_000), toolCost('Grep', null)],
  toolCount: 3,
  failing: [],
  holdHeadline: false,
  calls: [call('c1', { ts: '2026-11-01T11:59:00Z', model: 'claude-opus', status: 429, error_class: 'rate_limited' })],
  sessions: [
    {
      session_key: 's-1',
      key: 'k',
      client: 'claude-code',
      first_ts: '2026-11-01T11:00:00Z',
      last_ts: '2026-11-01T11:30:00Z',
      ended_at: '2026-11-01T11:31:00Z',
      calls: 12,
      prompts: 2,
      tool_calls: 4,
      errors: 2,
      agent_count: 1,
      models: ['claude-opus'],
      usage: { input_uncached: 1, cache_read: 0, cache_write_5m: 0, cache_write_1h: 0, output: 1, reasoning: 0 },
      cost_micro_usd: null,
    },
  ],
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
      const costPath = ['months', 'projects', 'punchcard', 'sessions', 'tools'].find((p) => url.startsWith(`/admin/usage/${p}`))
      if (costPath !== undefined) {
        if (f.failing.includes(costPath)) {
          return new Response(JSON.stringify({ error: { type: 'invalid_parameter', message: 'bad', parameter: costPath } }), { status: 400 })
        }
        if (costPath === 'months') return json({ tz: q.get('tz'), months: f.months })
        if (costPath === 'projects') return json(f.projects)
        if (costPath === 'punchcard') return json({ tz: q.get('tz'), cells: f.cells })
        if (costPath === 'tools') return json({ tools: f.tools, tool_count: f.toolCount })
        return json({ sessions: f.costSessions, session_count: f.sessionCount })
      }
      if (url.startsWith('/admin/usage')) {
        const group = q.get('group_by')
        if (f.holdHeadline && !group && q.get('from') === CURRENT_FROM) return new Promise<Response>(() => {})
        const buckets = group ? (f.byGroup[group] ?? []) : q.get('from') === CURRENT_FROM ? f.current : f.previous
        return json({ group_by: group, grain: q.get('grain'), buckets })
      }
      if (url.startsWith('/admin/config')) {
        return json({ providers: [{ name: 'anthropic', kind: 'anthropic', base_url: '', auth_mode: f.authMode, idle_timeout: '10m' }], pricing: [], keys: [] })
      }
      if (url.startsWith('/admin/sessions')) return json({ sessions: f.sessions, next_cursor: null })
      if (url.startsWith('/admin/calls')) {
        return json({ calls: f.calls, next_cursor: null, count: f.calls.length, count_capped: false })
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
  await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeTruthy())
  return { router, ...view }
}

const tile = (label: string) => screen.getByText(label, { selector: 'dt' }).parentElement as HTMLElement

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(NOW))
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

test('/ui/ is the overview, current in the sidebar', async () => {
  stubApi()
  const { router } = await renderAt('/ui/')
  expect(router.state.location.pathname).toBe('/')
  const nav = screen.getByRole('navigation', { name: 'Main' })
  await waitFor(() => expect(within(nav).getByRole('link', { name: 'Overview' }).getAttribute('aria-current')).toBe('page'))
  // The overview link is exact: the call log is one click away and not marked current.
  expect(within(nav).getByRole('link', { name: 'Calls' }).getAttribute('aria-current')).toBeNull()
})

test('headline numbers compare with the previous equal period', async () => {
  const urls = stubApi()
  await renderAt('/ui/')
  await waitFor(() => expect(within(tile('Calls')).getByText('60')).toBeTruthy())
  // 60 calls against 40: +50%. Cost $6.00 against $4.00.
  expect(tile('Calls').textContent).toContain('+50%')
  expect(tile('Calls').textContent).toContain('vs previous 24h')
  expect(tile('Cost').textContent).toContain('$6.00')
  expect(tile('Cost').querySelector('.change')?.getAttribute('data-tone')).toBe('bad')
  // 3 errors in 60 calls against none: a rise in points, marked with a glyph and words.
  expect(tile('Error rate').textContent).toContain('5.0%')
  expect(tile('Error rate').textContent).toContain('+5.0 pts')
  expect(tile('Cache hit').textContent).toContain('40.0%')
  expect(tile('Cache hit').textContent).toContain('no change')
  // Two buckets: the percentiles are marked approximate.
  expect(tile('Latency p95').textContent).toContain('≈ 400 ms')
  // The previous window is the 24h before the current one.
  expect(urls.some((u) => u.includes('from=2026-10-30T12%3A00%3A00.000Z') && u.includes('to=2026-10-31T12%3A00%3A00.000Z'))).toBe(true)
})

test('unknown cost shows as unknown, not zero', async () => {
  stubApi({ current: [bucket({ calls: 5, unpriced_calls: 5, cost_micro_usd: 0 })] })
  await renderAt('/ui/')
  await waitFor(() => expect(within(tile('Calls')).getByText('5')).toBeTruthy())
  expect(tile('Cost').querySelector('.kpi-value')?.textContent).toBe('unknown')
  expect(tile('Cost').textContent).not.toContain('$0.00')
})

test('client_passthrough cost carries the API-equivalent label', async () => {
  const urls = stubApi({ authMode: 'client_passthrough' })
  await renderAt('/ui/')
  await waitFor(() => expect(tile('Cost').textContent).toContain('API-equivalent'))
  const feed = await screen.findByRole('table', { name: 'Latest finished calls' })
  expect(within(feed).getByText(/API-equivalent/)).toBeTruthy()
  expect(urls.some((u) => u.includes('group_by=provider'))).toBe(true)
})

test('without a passthrough provider no cost is API-equivalent', async () => {
  const urls = stubApi()
  await renderAt('/ui/')
  await waitFor(() => expect(tile('Cost').textContent).toContain('$6.00'))
  await screen.findByRole('table', { name: 'Latest finished calls' })
  expect(screen.queryByText(/API-equivalent/)).toBeNull()
  expect(urls.some((u) => u.includes('group_by=provider'))).toBe(false)
})

test('problems group errors by class and link to the filtered call log', async () => {
  stubApi()
  const { router } = await renderAt('/ui/?range=7d')
  const list = await screen.findByRole('list', { name: 'Errors by class' })
  const items = within(list).getAllByRole('listitem')
  expect(items.map((li) => li.textContent)).toEqual(['Rate limited (429)2 calls', 'Upstream 5xx (500, 529 overloaded)1 call'])
  const link = within(items[0]).getByRole('link')
  expect(link.getAttribute('href')).toContain('/ui/calls?')
  fireEvent.click(link)
  await waitFor(() => expect(router.state.location.pathname).toBe('/calls'))
  expect(router.state.matches.at(-1)?.search).toEqual({ error_class: ['rate_limited'], range: '7d' })
})

test('no errors shows a calm empty state', async () => {
  stubApi({ byGroup: { error_class: [bucket({ group: '', errors: 0 })] } })
  await renderAt('/ui/')
  expect(await screen.findByText(/No errors in the last 24h/)).toBeTruthy()
})

test('live feed, recent sessions and breakdowns link to the other views', async () => {
  stubApi()
  await renderAt('/ui/')
  const feed = await screen.findByRole('table', { name: 'Latest finished calls' })
  expect(within(feed).getByText('429 · rate_limited')).toBeTruthy()
  FakeEventSource.instances[0].open()
  FakeEventSource.instances[0].finish(call('c2', { ts: '2026-11-01T12:00:30Z', model: 'claude-haiku' }))
  await waitFor(() => expect(within(feed).getAllByRole('row')).toHaveLength(3))
  expect(within(within(feed).getAllByRole('row')[1]).getByText('claude-haiku')).toBeTruthy()

  const sessions = await screen.findByRole('table', { name: 'Recent sessions' })
  expect(within(sessions).getByRole('link', { name: 's-1' }).getAttribute('href')).toBe('/ui/sessions/s-1')
  expect(within(sessions).getByText('2 errors')).toBeTruthy()
  expect(within(sessions).getByText('unknown')).toBeTruthy()

  const byModel = await screen.findByRole('list', { name: 'Cost by model' })
  expect(within(byModel).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
    'claude-opus$5.00 · 83%',
    'claude-haiku$1.00 · 17%',
  ])
})

test('the range is in the URL and moves both windows', async () => {
  const urls = stubApi()
  const { router } = await renderAt('/ui/')
  fireEvent.click(screen.getByRole('button', { name: '7d' }))
  await waitFor(() => expect(router.state.matches.at(-1)?.search).toEqual({ range: '7d' }))
  // The sidebar marks the page current whatever its search.
  const nav = screen.getByRole('navigation', { name: 'Main' })
  expect(within(nav).getByRole('link', { name: 'Overview' }).getAttribute('aria-current')).toBe('page')
  await waitFor(() => expect(urls.some((u) => u.includes('from=2026-10-25T12%3A00%3A00.000Z'))).toBe(true))
  // Only the dashboard presets are offered, and no absolute window.
  expect(screen.queryByRole('button', { name: '15m' })).toBeNull()
  expect(screen.queryByLabelText('From')).toBeNull()
})

test('the overview has no accessibility violations', async () => {
  stubApi()
  const { container } = await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Errors by class' })
  await screen.findByRole('table', { name: 'Recent sessions' })
  vi.useRealTimers()
  const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } })
  expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([])
})

// The cost section (ADR 0059).

const costUrls = (urls: string[], path: string) => urls.filter((u) => u.startsWith(`/admin/usage/${path}?`))
const param = (url: string, name: string) => new URL(url, 'http://x').searchParams.get(name)
const localParts = (iso: string | null) => {
  const d = new Date(iso ?? '')
  return [d.getFullYear(), d.getMonth() + 1, d.getDate(), d.getHours(), d.getMinutes()]
}
const costTile = (label: string) => screen.getByText(label, { selector: 'dt' }).parentElement as HTMLElement

test('the cost section does not ask for data before the headline tiles have rendered', async () => {
  const urls = stubApi({ holdHeadline: true })
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Errors by class' })
  // The tiles have not rendered, so the heavy queries have not started.
  expect(costUrls(urls, 'months')).toHaveLength(0)
  expect(costUrls(urls, 'projects')).toHaveLength(0)
})

test('the default period is three calendar months up to now, cut in the browser zone', async () => {
  const urls = stubApi()
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  const [months] = costUrls(urls, 'months')
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  expect(param(months, 'tz')).toBe(zone)
  // 1 November 2026 back to September: the start of the local month.
  expect(localParts(param(months, 'from'))).toEqual([2026, 9, 1, 0, 0])
  expect(param(months, 'to')).toBe(new Date(NOW).toISOString())
  expect(screen.getByText(`Months and hours in ${zone}`)).toBeTruthy()
  const [projects] = costUrls(urls, 'projects')
  expect(param(projects, 'limit')).toBe('10')
  expect(param(projects, 'from')).toBe(param(months, 'from'))
  // The punchcard is cut in the same zone, over the same window; the ranked lists are bounded on the server.
  const [punchcard] = costUrls(urls, 'punchcard')
  expect(param(punchcard, 'tz')).toBe(zone)
  expect(param(punchcard, 'from')).toBe(param(months, 'from'))
  expect(param(costUrls(urls, 'sessions')[0], 'limit')).toBe('10')
  expect(param(costUrls(urls, 'tools')[0], 'limit')).toBe('10')
  expect(param(costUrls(urls, 'tools')[0], 'from')).toBe(param(months, 'from'))
  // The overview no longer asks for one bucket per session and day.
  expect(urls.some((u) => u.includes('group_by=session'))).toBe(false)
})

test('the cost section is its own region with its own period control, and says the overview range does not apply', async () => {
  stubApi()
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  const section = screen.getByRole('region', { name: 'Cost' })
  expect(within(section).getByRole('group', { name: 'Cost period' })).toBeTruthy()
  expect(within(section).getByText(/overview time range above does not apply/)).toBeTruthy()
  // The overview picker (1h to 30d) is outside the section; the section holds only its own control.
  const overviewPicker = screen.getByRole('button', { name: '7d' })
  expect(section.contains(overviewPicker)).toBe(false)
  expect(within(section).queryByRole('button', { name: '7d' })).toBeNull()
})

test('the period is in the URL, is announced, and leaves the dashboard range alone', async () => {
  const urls = stubApi()
  const { router } = await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  expect(screen.getByRole('button', { name: '3 months' }).getAttribute('aria-pressed')).toBe('true')
  expect(screen.queryByText(/longer period can take longer/)).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '6 months' }))
  await waitFor(() => expect(router.state.matches.at(-1)?.search).toEqual({ cost_range: '6m' }))
  expect(screen.getByText('Showing the last 6 months')).toBeTruthy()
  await waitFor(() => expect(costUrls(urls, 'months')).toHaveLength(2))
  expect(localParts(param(costUrls(urls, 'months')[1], 'from'))).toEqual([2026, 6, 1, 0, 0])
  expect(screen.getByText(/longer period can take longer/)).toBeTruthy()
  // A dashboard range change moves the activity windows and not the cost aggregates.
  fireEvent.click(screen.getByRole('button', { name: '7d' }))
  await waitFor(() => expect(router.state.matches.at(-1)?.search).toEqual({ range: '7d', cost_range: '6m' }))
  await waitFor(() => expect(urls.some((u) => u.includes('from=2026-10-25T12%3A00%3A00.000Z'))).toBe(true))
  expect(costUrls(urls, 'months')).toHaveLength(2)
  // Back to the default: the parameter leaves the URL.
  fireEvent.click(screen.getByRole('button', { name: '3 months' }))
  await waitFor(() => expect(router.state.matches.at(-1)?.search).toEqual({ range: '7d' }))
})

test('the tiles name the most costly month and project with their share of the period', async () => {
  stubApi()
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  // October is $6 of the $10 known: 60%, and three of its calls are unpriced.
  const monthTile = costTile('Most costly month')
  expect(monthTile.textContent).toContain('October 2026')
  expect(monthTile.textContent).toContain('$6.00 · 60% of the period · partial')
  // The project has $5 of the $10 of the whole period, projects past the list and calls with no project included.
  const projectTile = costTile('Most costly project')
  expect(projectTile.textContent).toContain('spyglass')
  expect(projectTile.textContent).toContain('$5.00 · 50% of the period')
  // The router writes a list parameter as JSON, as every other link to the call log does.
  expect(within(projectTile).getByRole('link', { name: 'Call log' }).getAttribute('href')).toContain('project=%5B%22spyglass%22%5D')
})

test('the month chart and its table fallback hold the same numbers', async () => {
  stubApi()
  await renderAt('/ui/')
  const chart = await screen.findByRole('img', { name: 'Cost per month: $10.00 in 3 months; most costly October 2026, $6.00' })
  // Text marks, so the maximum and the running month are not told by colour or outline alone.
  expect(within(chart).getByText('highest')).toBeTruthy()
  expect(chart.textContent).toContain('so far')
  expect(chart.textContent).toContain('partial')
  const table = screen.getByRole('table', { name: 'Cost per month', hidden: true })
  const rows = within(table).getAllByRole('row', { hidden: true }).slice(1)
  expect(rows.map((r) => Array.from(r.children).map((c) => c.textContent))).toEqual([
    ['September 2026', '20', '$3.00', '0'],
    ['October 2026', '30', '$6.00', '3'],
    ['November 2026 so far', '4', '$1.00', '0'],
  ])
})

test('projects are ranked, link to the call log, and the calls with none get a row of their own', async () => {
  stubApi()
  const { router } = await renderAt('/ui/')
  const list = await screen.findByRole('list', { name: 'Cost per project' })
  expect(within(list).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
    'spyglass$5.00 · 50%',
    'docs-site$1.50 · 15%partial: 2 unpriced',
  ])
  // Not a project, so not in the ranking, but its calls and cost are not lost.
  expect(screen.getByText(/No project/).parentElement?.textContent).toBe('No project: $3.50 · 7 calls')
  fireEvent.click(within(list).getByRole('link', { name: 'docs-site' }))
  await waitFor(() => expect(router.state.location.pathname).toBe('/calls'))
  const search = router.state.matches.at(-1)?.search as { project: string[]; from: string; to: string }
  expect(search.project).toEqual(['docs-site'])
  expect(localParts(search.from)).toEqual([2026, 9, 1, 0, 0])
  expect(search.to).toBe(new Date(NOW).toISOString())
})

test('the project list says how many more projects there are', async () => {
  stubApi({ projects: { ...defaults.projects, project_count: 14 } })
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  expect(screen.getByText('12 more')).toBeTruthy()
})

test('this month at the current pace is a straight line from the month so far', async () => {
  // 14.5 of the 30 days of November have passed: $1.00 so far becomes $2.07, against $6.00 in October.
  vi.setSystemTime(new Date('2026-11-15T12:00:00Z'))
  stubApi()
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  const pace = costTile('This month at the current pace')
  expect(pace.querySelector('.kpi-value')?.textContent).toBe('$2.07')
  expect(pace.textContent).toContain('$1.00 so far in November 2026')
  expect(pace.textContent).toContain('−66% vs October 2026')
})

test('the pace waits for the month to be a day old', async () => {
  stubApi()
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  const pace = costTile('This month at the current pace')
  expect(pace.querySelector('.kpi-value')?.textContent).toBe('too early')
  expect(pace.textContent).toContain('$1.00 so far')
})

test('the average per session counts the known cost over the sessions, and says it is partial', async () => {
  stubApi()
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  // $10.00 over 5 sessions, and over the 51 priced calls of 54: $0.20.
  const tileText = costTile('Average cost per session')
  await waitFor(() => expect(tileText.querySelector('.kpi-value')?.textContent).toBe('$2.00'))
  expect(tileText.textContent).toContain('$0.20 per call')
  expect(tileText.textContent).toContain('partial: 3 unpriced')
})

test('a period with no calls says so in every panel and the tiles read no data', async () => {
  stubApi({
    months: [month('2026-09', { calls: 0, cost_micro_usd: 0 }), month('2026-10', { calls: 0, cost_micro_usd: 0 }), month('2026-11', { calls: 0, cost_micro_usd: 0, partial: true })],
    projects: { projects: [], project_count: 0, unassigned: { calls: 0, cost_micro_usd: 0, unpriced_calls: 0 } },
    cells: [],
    costSessions: [],
    sessionCount: 0,
    tools: [],
    toolCount: 0,
  })
  await renderAt('/ui/')
  await waitFor(() => expect(screen.getAllByText('No calls in the last 3 months.')).toHaveLength(3))
  expect(screen.getByText('No sessions in the last 3 months.')).toBeTruthy()
  expect(screen.getByText('No tool calls in this period.')).toBeTruthy()
  for (const label of ['Most costly month', 'Most costly project', 'This month at the current pace', 'Average cost per session']) {
    expect(costTile(label).textContent).toContain('no data')
    expect(costTile(label).textContent).not.toContain('$0.00')
  }
})

test('when no call has a price the cost is unknown, not zero, and the config page is one link away', async () => {
  const unknown = { cost_micro_usd: null, unpriced_calls: 10 }
  stubApi({
    months: [month('2026-10', { calls: 10, ...unknown }), month('2026-11', { calls: 10, partial: true, ...unknown })],
    projects: { projects: [projectRow('spyglass', null, { unpriced_calls: 10 })], project_count: 1, unassigned: { calls: 0, cost_micro_usd: 0, unpriced_calls: 0 } },
    cells: [cell(2, 14, null)],
    costSessions: [costSession('s-1', null)],
    tools: [toolCost('Bash', null)],
  })
  await renderAt('/ui/')
  // Months, projects, sessions and tools say so in a panel; the punchcard says so in a note under its markers.
  await waitFor(() => expect(screen.getAllByText(/Cost is unknown: no price matched the calls in this period/)).toHaveLength(5))
  expect(screen.getAllByRole('link', { name: 'Check the pricing in the config' })[0].getAttribute('href')).toBe('/ui/admin/config')
  expect(costTile('Most costly month').textContent).toContain('unknown')
  expect(costTile('Most costly project').textContent).toContain('unknown')
  expect(screen.queryByRole('img', { name: /Cost per month/ })).toBeNull()
  expect(screen.queryByRole('table', { name: 'Most costly sessions' })).toBeNull()
  expect(screen.queryByRole('table', { name: /Most costly tools/ })).toBeNull()
  // The punchcard still shows the hours that have calls, as hollow markers.
  const punchcard = screen.getByRole('img', { name: /Cost by weekday and hour/ })
  expect(punchcard.querySelectorAll('.punch-dot-unknown')).toHaveLength(1)
})

test('a month with no calls, padded with a cost of 0 by the API, does not make unpriced calls cost zero', async () => {
  // GET /admin/usage/months returns every month of the window; a month with no
  // calls has cost_micro_usd 0. Only the month with calls is unpriced.
  stubApi({
    months: [
      month('2026-09', { calls: 0, cost_micro_usd: 0 }),
      month('2026-10', { calls: 0, cost_micro_usd: 0 }),
      month('2026-11', { calls: 5, cost_micro_usd: null, unpriced_calls: 5, partial: true }),
    ],
    projects: { projects: [projectRow('spyglass', null, { unpriced_calls: 3 })], project_count: 1, unassigned: { calls: 2, cost_micro_usd: null, unpriced_calls: 2 } },
    sessionCount: 2,
    costSessions: [costSession('s-1', null)],
  })
  await renderAt('/ui/')
  await waitFor(() => expect(screen.getAllByText(/Cost is unknown: no price matched the calls in this period/)).toHaveLength(3))
  expect(costTile('Average cost per session').textContent).not.toContain('$0.00')
  expect(costTile('Average cost per session').textContent).toContain('unknown')
})

test('with no project recorded the panel explains where a project comes from and keeps the unassigned row', async () => {
  stubApi({ projects: { projects: [], project_count: 0, unassigned: { calls: 7, cost_micro_usd: 3_500_000, unpriced_calls: 0 } } })
  await renderAt('/ui/')
  expect(await screen.findByText('No calls have a project yet.')).toBeTruthy()
  expect(screen.getByText(/x-spyglass-project/)).toBeTruthy()
  expect(screen.getByText(/keys\[\]\.project/)).toBeTruthy()
  expect(screen.getByText(/No project/, { selector: 'span' }).parentElement?.textContent).toBe('No project: $3.50 · 7 calls')
  expect(costTile('Most costly project').textContent).toContain('no project recorded')
})

test('projects with no price still list, with a priced no-project row, when the period cost is known', async () => {
  stubApi({
    months: [month('2026-11', { calls: 17, cost_micro_usd: 3_500_000, unpriced_calls: 10, partial: true })],
    projects: { projects: [projectRow('spyglass', null, { unpriced_calls: 10 })], project_count: 1, unassigned: { calls: 7, cost_micro_usd: 3_500_000, unpriced_calls: 0 } },
  })
  await renderAt('/ui/')
  const list = await screen.findByRole('list', { name: 'Cost per project' })
  expect(within(list).getAllByRole('listitem').map((li) => li.textContent)).toEqual(['spyglassunknownpartial: 10 unpriced'])
  expect(screen.getByText(/No project/, { selector: 'span' }).parentElement?.textContent).toBe('No project: $3.50 · 7 calls')
  expect(screen.queryByText(/Cost is unknown: no price matched the calls in this period/)).toBeNull()
})

test('without the period cost the project tile and list show no share', async () => {
  stubApi({ failing: ['months'] })
  await renderAt('/ui/')
  const list = await screen.findByRole('list', { name: 'Cost per project' })
  expect(within(list).getAllByRole('listitem').map((li) => li.textContent)).toEqual([
    'spyglass$5.00',
    'docs-site$1.50partial: 2 unpriced',
  ])
  const projectTile = costTile('Most costly project')
  expect(projectTile.textContent).toContain('$5.00')
  expect(projectTile.textContent).not.toContain('of the period')
})

test('a month with no calls is a labelled gap,and one month of data still has a most costly month', async () => {
  stubApi({ months: [month('2026-09', { calls: 0, cost_micro_usd: 0 }), month('2026-10', { calls: 0, cost_micro_usd: 0 }), month('2026-11', { calls: 4, cost_micro_usd: 2_000_000, partial: true })] })
  await renderAt('/ui/')
  const chart = await screen.findByRole('img', { name: /Cost per month/ })
  expect(Array.from(chart.querySelectorAll('.month-value')).map((t) => t.textContent)).toEqual(['no calls', 'no calls', '$2.00'])
  const table = screen.getByRole('table', { name: 'Cost per month', hidden: true })
  expect(within(table).getByText('September 2026').closest('tr')?.textContent).toContain('no calls')
  const tile = costTile('Most costly month')
  expect(tile.textContent).toContain('November 2026')
  expect(tile.textContent).toContain('100% of the period')
})

test('a failing panel shows its own error with Retry and leaves the others alone', async () => {
  const failing = ['projects']
  stubApi({ failing })
  await renderAt('/ui/')
  const alert = (await screen.findAllByText('The filter "projects" is not valid.'))[0]
  expect(await screen.findByRole('img', { name: /Cost per month/ })).toBeTruthy()
  expect(costTile('Most costly month').textContent).toContain('October 2026')
  expect(costTile('Most costly project').textContent).toContain('The filter "projects" is not valid.')
  failing.length = 0
  fireEvent.click(within(alert.closest('[role="alert"]') as HTMLElement).getByRole('button', { name: 'Retry' }))
  expect(await screen.findByRole('list', { name: 'Cost per project' })).toBeTruthy()
})

test('API-equivalent cost in the section carries the same note as the breakdowns', async () => {
  stubApi({ months: defaults.months.map((m) => ({ ...m, api_equivalent: true })) })
  await renderAt('/ui/')
  await screen.findByRole('list', { name: 'Cost per project' })
  expect(screen.getByText(/Includes API-equivalent cost of client_passthrough providers\. client_passthrough:/)).toBeTruthy()
})

// Punchcard, sessions and tools (ADR 0059, phase 11).

const radii = (svg: HTMLElement) => Object.fromEntries(Array.from(svg.querySelectorAll('.punch-cell')).map((g) => [g.querySelector('title')?.textContent?.split(',')[0], Number(g.querySelector('circle')?.getAttribute('r'))]))

test('the punchcard sizes a marker by cost, marks the unknown hour as hollow, and names the most costly hour', async () => {
  stubApi()
  await renderAt('/ui/')
  const chart = await screen.findByRole('img', { name: 'Cost by weekday and hour: $3.50 in the period; most costly Tuesday 14:00–14:59, $2.00' })
  const r = radii(chart)
  // $2.00 against $0.50: four times the area, so twice the radius. $1.00 is half the area of $2.00.
  expect(r['Tuesday 14:00–14:59'] / r['Monday 09:00–09:59']).toBeCloseTo(2, 5)
  expect(r['Sunday 00:00–00:59'] ** 2 / r['Tuesday 14:00–14:59'] ** 2).toBeCloseTo(0.5, 5)
  // An hour with calls and no price is a hollow marker that says so, not a zero.
  const unknown = within(chart).getByText('Saturday 22:00–22:59, cost unknown, 10 calls', { selector: 'title' })
  expect(unknown.parentElement?.querySelector('circle')?.getAttribute('class')).toContain('punch-dot-unknown')
  // The maximum is said in words and marked by an outline, not by colour alone.
  expect(chart.querySelectorAll('.punch-dot-best')).toHaveLength(1)
  expect(screen.getByText(/Most costly hour: Tuesday 14:00–14:59, \$2\.00/)).toBeTruthy()
  // Rows run Monday to Sunday and every third hour is labelled.
  expect(Array.from(chart.querySelectorAll('.punch-axis')).map((t) => t.textContent)).toEqual([
    'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun', '00', '03', '06', '09', '12', '15', '18', '21',
  ])
})

test('the punchcard table fallback lists every hour with its numbers, the most costly first', async () => {
  stubApi()
  await renderAt('/ui/')
  await screen.findByRole('img', { name: /Cost by weekday and hour/ })
  const table = screen.getByRole('table', { name: /Cost by weekday and hour, highest first/, hidden: true })
  const rows = within(table).getAllByRole('row', { hidden: true }).slice(1)
  expect(rows.map((row) => Array.from(row.children).map((c) => c.textContent))).toEqual([
    ['Tuesday', '14:00–14:59', '10', '$2.00', '0'],
    ['Sunday', '00:00–00:59', '10', '$1.00', '0'],
    ['Monday', '09:00–09:59', '10', '$0.50', '0'],
    ['Saturday', '22:00–22:59', '10', 'unknown', '10'],
  ])
})

test('most costly sessions come from the ranked endpoint and keep the numbers of the session list', async () => {
  stubApi()
  const { router } = await renderAt('/ui/')
  const table = await screen.findByRole('table', { name: 'Most costly sessions' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows.map((row) => Array.from(row.children).map((c) => c.textContent))).toEqual([
    ['s-big', '12', '$4.00', 'none'],
    ['s-small', '12', '$1.50 · partial: 2 unpriced', '3'],
  ])
  // The list is shorter than the whole, and says by how much.
  expect(screen.getByText('3 more')).toBeTruthy()
  fireEvent.click(within(table).getByRole('link', { name: 's-small' }))
  await waitFor(() => expect(router.state.location.pathname).toBe('/sessions/s-small'))
})

test('most costly tools are an estimate, labelled so, with unknown cost kept apart from zero', async () => {
  stubApi()
  const { router } = await renderAt('/ui/')
  const table = await screen.findByRole('table', { name: /Most costly tools/ })
  expect(within(table).getByRole('columnheader', { name: /Estimated cost of the calls that requested it/ })).toBeTruthy()
  expect(within(table).getByRole('note', { hidden: true }).textContent).toBe(
    "An estimate: a call's cost is split equally among its tool uses. This is not the cost the tool's result adds to later calls.",
  )
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows.map((row) => Array.from(row.children).map((c) => c.textContent))).toEqual([
    ['Bash', '20', '2', '$3.00'],
    ['Read', '8', 'none', '$1.00'],
    ['Grep', '8', 'none', 'unknown'],
  ])
  fireEvent.click(within(table).getByRole('link', { name: 'Grep' }))
  await waitFor(() => expect(router.state.location.pathname).toBe('/tools'))
  const search = router.state.matches.at(-1)?.search as { tool: string[]; from: string }
  expect(search.tool).toEqual(['Grep'])
  expect(localParts(search.from)).toEqual([2026, 9, 1, 0, 0])
})

test('a failing punchcard, sessions or tools panel shows its own error and leaves the rest alone', async () => {
  stubApi({ failing: ['punchcard', 'tools'] })
  await renderAt('/ui/')
  expect(await screen.findByRole('table', { name: 'Most costly sessions' })).toBeTruthy()
  expect(screen.getByRole('img', { name: /Cost per month/ })).toBeTruthy()
  expect(await screen.findAllByText('The filter "punchcard" is not valid.')).toHaveLength(1)
  expect(screen.getAllByText('The filter "tools" is not valid.')).toHaveLength(1)
  expect(screen.queryByRole('img', { name: /Cost by weekday and hour/ })).toBeNull()
})

test('when the sessions query fails the average per session shows the error, not a number made from the months alone', async () => {
  stubApi({ failing: ['sessions'] })
  await renderAt('/ui/')
  await screen.findByRole('img', { name: /Cost per month/ })
  const tile = costTile('Average cost per session')
  await waitFor(() => expect(tile.textContent).toContain('The filter "sessions" is not valid.'))
  expect(tile.textContent).not.toContain('$')
})

test('API-equivalent cost in a tool, session or hour gives the section the same note', async () => {
  stubApi({ tools: [toolCost('Bash', 3_000_000, { api_equivalent: true })], toolCount: 1 })
  await renderAt('/ui/')
  await screen.findByRole('table', { name: /Most costly tools/ })
  expect(screen.getByText(/Includes API-equivalent cost of client_passthrough providers\./)).toBeTruthy()
})

for (const theme of ['light', 'dark'] as const) {
  test(`the cost section has no accessibility violations in the ${theme} theme`, async () => {
    document.documentElement.dataset.theme = theme
    try {
      stubApi()
      const { container } = await renderAt('/ui/')
      await screen.findByRole('img', { name: /Cost by weekday and hour/ })
      await screen.findByRole('table', { name: 'Most costly sessions' })
      await screen.findByRole('table', { name: /Most costly tools/ })
      vi.useRealTimers()
      // jsdom has no layout or colour resolution: the contrast of the marks is checked in a browser.
      const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } })
      expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([])
    } finally {
      delete document.documentElement.dataset.theme
    }
  })
}
