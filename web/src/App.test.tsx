import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { createMemoryHistory } from '@tanstack/react-router'
import axe from 'axe-core'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { App } from './App'
import { createQueryClient } from './queryClient'
import { createAppRouter } from './router'

// The overview opens the live stream; jsdom has no EventSource.
class StubEventSource {
  static CLOSED = 2
  readyState = 0
  onopen = null
  onerror = null
  addEventListener() {}
  close() {}
}
beforeEach(() => vi.stubGlobal('EventSource', StubEventSource))
afterEach(() => vi.unstubAllGlobals())

async function renderAt(path: string) {
  const queryClient = createQueryClient()
  const history = createMemoryHistory({ initialEntries: [path] })
  const router = createAppRouter(queryClient, history)
  const view = render(<App queryClient={queryClient} router={router} />)
  await waitFor(() => expect(screen.getByRole('main')).toBeTruthy())
  return { router, ...view }
}

test('/ui/ is the overview dashboard, not a redirect', async () => {
  const { router } = await renderAt('/ui/')
  await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Overview' })).toBeTruthy())
  expect(router.state.location.pathname).toBe('/')
})

test.each([
  ['/ui/', 'Overview'],
  ['/ui/calls', 'Calls'],
  ['/ui/calls/0190a1b2-0000-7000-8000-000000000000', 'Call'],
  ['/ui/sessions', 'Sessions'],
  ['/ui/sessions/a%2Fb%3Ac', 'Session'],
  ['/ui/tools', 'Tools'],
  ['/ui/cost', 'Cost'],
  ['/ui/admin/config', 'Config'],
])('route %s renders', async (path, title) => {
  await renderAt(path)
  await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: title })).toBeTruthy())
})

test('the sidebar links every page', async () => {
  await renderAt('/ui/')
  const nav = screen.getByRole('navigation', { name: 'Main' })
  const labels = within(nav)
    .getAllByRole('link')
    .map((a) => a.textContent)
  expect(labels).toEqual(['Overview', 'Calls', 'Sessions', 'Tools', 'Cost', 'Config'])
})

test('an unknown path falls back to the overview', async () => {
  const { router } = await renderAt('/ui/nope')
  await waitFor(() => expect(router.state.location.pathname).toBe('/'))
})

test('a stale URL degrades to defaults instead of failing', async () => {
  const { router } = await renderAt('/ui/calls?range=%22nope%22&group=%22model%22&status=%5B%22x%22%5D')
  await waitFor(() => expect(screen.getByRole('heading', { name: 'Calls' })).toBeTruthy())
  // location.search is the raw URL; the match holds the validated search.
  expect(router.state.matches.at(-1)?.search).toEqual({})
})

test('the session key survives the URL with / and : in it', async () => {
  const { router } = await renderAt('/ui/sessions/a%2Fb%3Ac')
  await waitFor(() => expect(screen.getByRole('heading', { name: 'Session' })).toBeTruthy())
  expect(router.state.matches.at(-1)?.params).toEqual({ key: 'a/b:c' })
})

test('the layout has landmarks, a skip link and current-page navigation', async () => {
  await renderAt('/ui/sessions')
  expect(screen.getByRole('navigation', { name: 'Main' })).toBeTruthy()
  expect(screen.getByRole('banner')).toBeTruthy()
  expect(screen.getByRole('link', { name: 'Skip to content' }).getAttribute('href')).toBe('#main')
  await waitFor(() =>
    expect(screen.getByRole('link', { name: 'Sessions' }).getAttribute('aria-current')).toBe('page'),
  )
  expect(screen.getByRole('link', { name: 'Calls' }).getAttribute('aria-current')).toBeNull()
  expect(screen.getByRole('note').textContent).toBe('Content is not stored')
})

test('the sidebar carries the Spyglass brand and the theme toggle', async () => {
  await renderAt('/ui/calls')
  const banner = screen.getByRole('banner')
  expect(banner.querySelector('.brand')?.textContent).toBe('Spyglass')
  expect(within(banner).getByRole('group', { name: 'Theme' })).toBeTruthy()
})

describe.each(['light', 'dark'])('in the %s theme', (theme) => {
  beforeEach(() => window.localStorage.setItem('spyglass-theme', theme))
  afterEach(() => {
    window.localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
  })

  test('the layout has no accessibility violations', async () => {
    const { container } = await renderAt('/ui/calls')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Calls' })).toBeTruthy())
    expect(document.documentElement.getAttribute('data-theme')).toBe(theme)
    // jsdom has no layout or styles, so colour contrast cannot be computed.
    const results = await axe.run(container, { rules: { 'color-contrast': { enabled: false } } })
    expect(results.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([])
  })
})

test.each([
  ['/ui/', 'Overview · Spyglass'],
  ['/ui/calls', 'Calls · Spyglass'],
  ['/ui/calls/0190a1b2-0000-7000-8000-000000000000', 'Call 0190a1b2-0000-7000-8000-000000000000 · Spyglass'],
  ['/ui/sessions', 'Sessions · Spyglass'],
  ['/ui/sessions/a%2Fb%3Ac', 'Session a/b:c · Spyglass'],
  ['/ui/tools', 'Tools · Spyglass'],
  ['/ui/cost', 'Cost · Spyglass'],
  ['/ui/admin/config', 'Config · Spyglass'],
  ['/ui/nope', 'Overview · Spyglass'],
])('route %s has the title %s', async (path, title) => {
  await renderAt(path)
  await waitFor(() => expect(document.title).toBe(title))
})

describe('focus on navigation', () => {
  test('the first load does not move focus', async () => {
    await renderAt('/ui/calls')
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Calls' })).toBeTruthy())
    expect(document.activeElement).toBe(document.body)
  })

  test('a link click moves focus to the new <h1> and updates the title', async () => {
    await renderAt('/ui/calls')
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Calls' })).toBeTruthy())
    fireEvent.click(screen.getByRole('link', { name: 'Sessions' }))
    const h1 = await screen.findByRole('heading', { level: 1, name: 'Sessions' })
    await waitFor(() => expect(document.activeElement).toBe(h1))
    expect(h1.getAttribute('tabindex')).toBe('-1')
    expect(document.title).toBe('Sessions · Spyglass')
  })

  test('a change of search parameters only does not move focus', async () => {
    const { router } = await renderAt('/ui/calls')
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Calls' })).toBeTruthy())
    const sidebarLink = screen.getByRole('link', { name: 'Cost' })
    sidebarLink.focus()
    await act(() => router.navigate({ to: '/calls', search: { range: '1h' } }))
    await waitFor(() => expect(router.state.location.searchStr).not.toBe(''))
    expect(document.activeElement).toBe(sidebarLink)
    expect(document.title).toBe('Calls · Spyglass')
  })
})
