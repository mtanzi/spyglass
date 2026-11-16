import { useEffect } from 'react'
import { Link, Outlet, useRouter, useRouterState } from '@tanstack/react-router'
import { SpyglassIcon } from './SpyglassIcon'
import { ThemeToggle } from './ThemeToggle'

// The overview (ADR 0052), the views, the cost page and the read-only config page (ADR 0035).
const NAV = [
  { to: '/', label: 'Overview' },
  { to: '/calls', label: 'Calls' },
  { to: '/sessions', label: 'Sessions' },
  { to: '/tools', label: 'Tools' },
  { to: '/cost', label: 'Cost' },
  { to: '/admin/config', label: 'Config' },
] as const

/**
 * Sets the document title from the deepest matched route, unique part first
 * ("Sessions · Spyglass"), and moves focus to the new view's <h1> after a
 * client-side navigation to another path (ADR 0058). The first load and a
 * change of search parameters only (a filter, a sort, a range) keep focus.
 */
function useRouteAnnouncement() {
  const title = useRouterState({
    select: (s) => {
      const match = s.matches.at(-1)
      return match?.staticData.title?.(match.params as Record<string, string>)
    },
  })
  useEffect(() => {
    if (title) document.title = `${title} · Spyglass`
  }, [title])
  const router = useRouter()
  useEffect(
    () =>
      // onRendered fires after the new view is in the DOM. fromLocation is
      // unset on the first load.
      router.subscribe('onRendered', ({ fromLocation, toLocation }) => {
        if (!fromLocation || fromLocation.pathname === toLocation.pathname) return
        document.querySelector<HTMLElement>('main h1')?.focus()
      }),
    [router],
  )
}

/**
 * Shared shell: skip link, a Linear-style sidebar with the Spyglass brand,
 * the main navigation (the current page is marked with aria-current and the
 * accent) and the theme toggle in its footer, and the main landmark beside it.
 */
export function Layout() {
  useRouteAnnouncement()
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="app-sidebar">
        <span className="brand">
          <SpyglassIcon />
          Spyglass
        </span>
        <nav aria-label="Main">
          <ul>
            {NAV.map((item) => (
              <li key={item.to}>
                <Link
                  to={item.to}
                  activeOptions={{ exact: item.to === '/', includeSearch: false }}
                  activeProps={{ 'aria-current': 'page' }}
                >
                  {item.label}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
        <div className="sidebar-foot">
          <ThemeToggle />
        </div>
      </header>
      <main id="main" tabIndex={-1}>
        <Outlet />
      </main>
    </div>
  )
}
