import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory } from '@tanstack/react-router'
import axe from 'axe-core'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { App } from '../App'
import { createQueryClient } from '../queryClient'
import { createAppRouter } from '../router'

class StubEventSource {
  static CLOSED = 2
  readyState = 0
  onopen = null
  onerror = null
  addEventListener() {}
  close() {}
}

const HASH = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'

// The API sends no hash and no credential. The extra fields here are the worst
// case: a server that leaked them. The page must still not render them.
const config = {
  providers: [
    { name: 'anthropic', kind: 'anthropic', base_url: 'https://api.anthropic.com', auth_mode: 'proxy_key', idle_timeout: '10m', api_key: 'sk-ant-LEAK' },
    { name: 'anthropic-plan', kind: 'anthropic', base_url: 'https://api.anthropic.com', auth_mode: 'client_passthrough', idle_timeout: '10m' },
  ],
  pricing: [
    {
      provider: 'anthropic',
      model: 'claude-opus',
      from: '2026-01-01',
      price: { input: 15_000_000, output: 75_000_000, cache_read: 1_500_000, cache_write_5m: 18_750_000, cache_write_1h: 30_000_000 },
    },
  ],
  keys: [
    { name: 'fm:task-a', disabled: false, hash: HASH, key_hash: HASH },
    { name: 'retired', disabled: true, hash: HASH },
  ],
}

function stubConfig(body: unknown = config, status = 200) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), { status })))
}

async function renderAt(path = '/ui/admin/config') {
  const queryClient = createQueryClient()
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  const view = render(<App queryClient={queryClient} router={router} />)
  await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Config' })).toBeTruthy())
  return { router, ...view }
}

beforeEach(() => vi.stubGlobal('EventSource', StubEventSource))
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

test('the config page shows a loading state, then the three tables', async () => {
  let release!: () => void
  const gate = new Promise<void>((resolve) => (release = resolve))
  vi.stubGlobal('fetch', vi.fn(async () => {
    await gate
    return new Response(JSON.stringify(config))
  }))
  await renderAt()
  expect(screen.getByText(/Loading config/)).toBeTruthy()
  expect(screen.queryByRole('status')).toBeNull()
  release()
  await screen.findByRole('table', { name: 'Providers' })
  expect(screen.queryByText(/Loading config/)).toBeNull()
  expect(screen.getByRole('table', { name: 'Pricing in USD per million tokens' })).toBeTruthy()
  expect(screen.getByRole('table', { name: 'Key names' })).toBeTruthy()
})

test('providers show their auth mode, and passthrough says the cost is API-equivalent', async () => {
  stubConfig()
  await renderAt()
  const table = await screen.findByRole('table', { name: 'Providers' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows[0].textContent).toContain('anthropic')
  expect(rows[0].textContent).toContain('proxy_key')
  expect(rows[0].textContent).not.toContain('API-equivalent')
  expect(rows[1].textContent).toContain('client_passthrough')
  expect(rows[1].textContent).toContain('cost is API-equivalent')
})

test('pricing is in dollars per million tokens', async () => {
  stubConfig()
  await renderAt()
  const table = await screen.findByRole('table', { name: 'Pricing in USD per million tokens' })
  const row = within(table).getAllByRole('row')[1]
  expect(row.textContent).toContain('claude-opus')
  expect(row.textContent).toContain('$15.00')
  expect(row.textContent).toContain('$75.00')
  expect(row.textContent).toContain('$1.50')
  expect(row.textContent).toContain('$18.75')
})

test('keys show their name and the disabled flag with a word, not colour alone', async () => {
  stubConfig()
  await renderAt()
  const table = await screen.findByRole('table', { name: 'Key names' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows[0].textContent).toContain('fm:task-a')
  expect(rows[0].textContent).toContain('enabled')
  expect(rows[1].textContent).toContain('retired')
  expect(rows[1].textContent).toContain('disabled')
})

test('no hash and no credential is rendered, even if the response carries one', async () => {
  stubConfig()
  const { container } = await renderAt()
  await screen.findByRole('table', { name: 'Key names' })
  const text = container.textContent ?? ''
  expect(text).not.toContain(HASH)
  expect(text).not.toContain('sk-ant-LEAK')
  expect(text).not.toMatch(/[0-9a-f]{32,}/)
  // No table or cell names a hash.
  const tables = screen.getAllByRole('table').map((t) => t.textContent?.toLowerCase() ?? '')
  expect(tables.join(' ')).not.toContain('hash')
  // Not in an attribute either.
  expect(container.innerHTML).not.toContain(HASH)
  expect(container.innerHTML).not.toContain('sk-ant-LEAK')
})

test('the page is read-only and explains how to change keys', async () => {
  stubConfig()
  await renderAt()
  await screen.findByRole('table', { name: 'Key names' })
  const main = screen.getByRole('main')
  expect(main.textContent).toContain('edit the config file and send SIGHUP')
  // Only the refresh button: no key management, no editing.
  expect(within(main).getAllByRole('button').map((b) => b.textContent)).toEqual(['Refresh'])
  expect(within(main).queryByRole('textbox')).toBeNull()
})

test('empty sections say so; with no prices the cost is unknown', async () => {
  stubConfig({ providers: [], pricing: [], keys: [] })
  await renderAt()
  await screen.findByText('No providers are configured.')
  expect(screen.getByText(/every call has an unknown cost/)).toBeTruthy()
  expect(screen.getByText('No keys are configured.')).toBeTruthy()
})

test('an API error shows with a retry, and Retry loads the config', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'bad_request', message: 'nope' } }), { status: 400 }))
    .mockResolvedValue(new Response(JSON.stringify(config)))
  vi.stubGlobal('fetch', fetchMock)
  await renderAt()
  const alert = await screen.findByRole('alert')
  fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
  await screen.findByRole('table', { name: 'Providers' }, { timeout: 5000 })
})

test('the sidebar links Config and marks it current', async () => {
  stubConfig()
  await renderAt()
  const nav = screen.getByRole('navigation', { name: 'Main' })
  expect(within(nav).getByRole('link', { name: 'Config' }).getAttribute('aria-current')).toBe('page')
})

test('the page has no accessibility violations', async () => {
  stubConfig()
  const { container } = await renderAt()
  await screen.findByRole('table', { name: 'Key names' })
  const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } })
  expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([])
})
