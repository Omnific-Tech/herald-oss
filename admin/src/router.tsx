import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  HeadContent,
  Outlet,
  redirect,
} from '@tanstack/react-router'
import { isAuthError } from '@/lib/api'
import { sessionQuery } from '@/lib/queries'
import { AnalyticsPage } from '@/pages/analytics'
import { AppLayout, AppLoadError } from '@/pages/app-layout'
import { ApplicationPage } from '@/pages/application'
import { ApplicationsPage } from '@/pages/applications'
import { LoginPage, type LoginReason } from '@/pages/login'
import { NewApplicationPage } from '@/pages/new-application'
import { NotFoundPage } from '@/pages/not-found'
import { NotificationsPage } from '@/pages/notifications'
import { OverviewPage } from '@/pages/overview'
import { SendNotificationPage } from '@/pages/send-notification'

type AppMeta = { authRedirect?: boolean }

declare module '@tanstack/react-query' {
  interface Register {
    queryMeta: AppMeta
    mutationMeta: AppMeta
  }
}

function handleAuthFailure(error: Error, meta?: AppMeta) {
  if (meta?.authRedirect === false || !isAuthError(error)) return
  queryClient.clear()
  void router.navigate({
    to: '/login',
    search: { reason: error.status === 401 ? 'expired' : 'forbidden' },
  })
}

export const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: (error, query) => handleAuthFailure(error, query.meta) }),
  mutationCache: new MutationCache({
    onError: (error, _variables, _context, mutation) => handleAuthFailure(error, mutation.meta),
  }),
  defaultOptions: { queries: { retry: false } },
})

const title = (page: string) => ({ meta: [{ title: `Admin | ${page}` }] })

const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: () => (
    <>
      <HeadContent />
      <Outlet />
    </>
  ),
  notFoundComponent: NotFoundPage,
  head: () => ({ meta: [{ title: 'Admin' }] }),
})

const reasons: LoginReason[] = ['expired', 'forbidden', 'signed-out']

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  head: () => title('Sign in'),
  validateSearch: (search: Record<string, unknown>): { reason?: LoginReason } => ({
    reason: reasons.find((reason) => reason === search.reason),
  }),
  component: LoginPage,
})

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  beforeLoad: async ({ context }) => {
    try {
      await context.queryClient.ensureQueryData(sessionQuery)
    } catch (error) {
      if (isAuthError(error)) throw redirect({ to: '/login' })
      throw error
    }
  },
  component: AppLayout,
  errorComponent: AppLoadError,
})

const overviewRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/',
  head: () => title('Overview'),
  component: OverviewPage,
})

const applicationsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/applications',
  head: () => title('Applications'),
  component: ApplicationsPage,
})

const newApplicationRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/applications/new',
  head: () => title('New application'),
  component: NewApplicationPage,
})

const applicationRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/applications/$slug',
  head: ({ params }) => title(params.slug),
  component: ApplicationPage,
})

const sendNotificationRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/applications/$slug/notify',
  head: () => title('Send notification'),
  component: SendNotificationPage,
})

const notificationsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/notifications',
  head: () => title('Notifications'),
  validateSearch: (search: Record<string, unknown>): { app?: string } => ({
    app: typeof search.app === 'string' ? search.app : undefined,
  }),
  component: NotificationsPage,
})

const analyticsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/analytics',
  head: () => title('Analytics'),
  component: AnalyticsPage,
})

export const router = createRouter({
  routeTree: rootRoute.addChildren([
    loginRoute,
    appRoute.addChildren([
      overviewRoute,
      applicationsRoute,
      newApplicationRoute,
      applicationRoute,
      sendNotificationRoute,
      notificationsRoute,
      analyticsRoute,
    ]),
  ]),
  context: { queryClient },
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
