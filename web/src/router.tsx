import { QueryClient } from '@tanstack/react-query'
import {
  Outlet,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  redirect,
} from '@tanstack/react-router'
import type { RouterHistory } from '@tanstack/react-router'
import { Layout } from './components/Layout'
import { CallDetailView } from './views/CallDetail'
import { CallLogView } from './views/CallLog'
import { ConfigView } from './views/ConfigView'
import { CostView } from './views/CostView'
import { DashboardView } from './views/Dashboard'
import { SessionListView } from './views/SessionList'
import { SessionView } from './views/SessionView'
import { ToolExplorerView } from './views/ToolExplorer'
import { strict } from './search/validate'
import {
  validateCallDetailSearch,
  validateCallsSearch,
  validateCostSearch,
  validateDashboardSearch,
  validateNoSearch,
  validateSessionDetailSearch,
  validateSessionsSearch,
  validateToolsSearch,
} from './search/routes'

declare module '@tanstack/react-router' {
  // The page title of a route, unique part first (WCAG 2.4.2). Layout joins it
  // with the app name. A route without one (the not-found redirect) sets none.
  interface StaticDataRouteOption {
    title?: (params: Record<string, string>) => string
  }
}

export interface RouterContext {
  queryClient: QueryClient
}

const rootRoute = createRootRouteWithContext<RouterContext>()({ component: Layout })

// The SPA is mounted at /ui/ (ADR 0029); the router's basepath is /ui, so
// these paths carry no prefix.
// The overview dashboard is the landing page (ADR 0052).
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  validateSearch: strict(validateDashboardSearch),
  staticData: { title: () => 'Overview' },
  component: DashboardView,
})

const callsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'calls',
  component: Outlet,
})
const callsIndexRoute = createRoute({
  getParentRoute: () => callsRoute,
  path: '/',
  validateSearch: strict(validateCallsSearch),
  staticData: { title: () => 'Calls' },
  component: CallLogView,
})
const callDetailRoute = createRoute({
  getParentRoute: () => callsRoute,
  path: '$id',
  validateSearch: strict(validateCallDetailSearch),
  staticData: { title: (p) => `Call ${p.id}` },
  component: CallDetailView,
})

const sessionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'sessions',
  component: Outlet,
})
const sessionsIndexRoute = createRoute({
  getParentRoute: () => sessionsRoute,
  path: '/',
  validateSearch: strict(validateSessionsSearch),
  staticData: { title: () => 'Sessions' },
  component: SessionListView,
})
const sessionDetailRoute = createRoute({
  getParentRoute: () => sessionsRoute,
  path: '$key',
  validateSearch: strict(validateSessionDetailSearch),
  staticData: { title: (p) => `Session ${p.key}` },
  component: SessionView,
})

const toolsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'tools',
  validateSearch: strict(validateToolsSearch),
  staticData: { title: () => 'Tools' },
  component: ToolExplorerView,
})

const costRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'cost',
  validateSearch: strict(validateCostSearch),
  staticData: { title: () => 'Cost' },
  component: CostView,
})

const configRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'admin/config',
  validateSearch: strict(validateNoSearch),
  staticData: { title: () => 'Config' },
  component: ConfigView,
})

const notFoundRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '$',
  beforeLoad: () => {
    throw redirect({ to: '/', search: {}, replace: true })
  },
})

export const routeTree = rootRoute.addChildren([
  indexRoute,
  callsRoute.addChildren([callsIndexRoute, callDetailRoute]),
  sessionsRoute.addChildren([sessionsIndexRoute, sessionDetailRoute]),
  toolsRoute,
  costRoute,
  configRoute,
  notFoundRoute,
])

export function createAppRouter(queryClient: QueryClient, history?: RouterHistory) {
  return createRouter({
    routeTree,
    history,
    basepath: '/ui',
    context: { queryClient },
    defaultPreload: 'intent',
  })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
