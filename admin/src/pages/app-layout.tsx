import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Outlet, useNavigate, useRouter, type ErrorComponentProps } from '@tanstack/react-router'
import { QueryError } from '@/components/query-error'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { sessionQuery } from '@/lib/queries'

const navLinkClass = 'rounded-md px-2.5 py-1.5 text-sm text-muted-foreground hover:text-foreground'
const activeNavLinkClass = 'bg-muted font-medium text-foreground'

export function AppLayout() {
  const { data: session } = useQuery(sessionQuery)
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const signOut = useMutation({
    mutationFn: api.signOut,
    meta: { authRedirect: false },
    onSettled: () => {
      queryClient.clear()
      void navigate({ to: '/login', search: { reason: 'signed-out' } })
    },
  })

  return (
    <div className="min-h-svh bg-muted/40">
      <header className="border-b bg-background">
        <div className="mx-auto flex max-w-5xl flex-wrap items-center gap-4 px-4 py-3">
          <span className="font-semibold">Admin</span>
          <nav className="flex gap-1">
            <Link
              to="/"
              className={navLinkClass}
              activeProps={{ className: activeNavLinkClass }}
              activeOptions={{ exact: true }}
            >
              Overview
            </Link>
            <Link to="/applications" className={navLinkClass} activeProps={{ className: activeNavLinkClass }}>
              Applications
            </Link>
            <Link to="/notifications" className={navLinkClass} activeProps={{ className: activeNavLinkClass }}>
              Notifications
            </Link>
            <Link to="/analytics" className={navLinkClass} activeProps={{ className: activeNavLinkClass }}>
              Analytics
            </Link>
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <span className="text-sm text-muted-foreground">{session?.username}</span>
            <Button variant="outline" size="sm" disabled={signOut.isPending} onClick={() => signOut.mutate()}>
              Sign out
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  )
}

export function AppLoadError({ error }: ErrorComponentProps) {
  const router = useRouter()
  return (
    <main className="mx-auto max-w-md p-8">
      <QueryError
        error={error instanceof Error ? error : new Error('Unexpected error.')}
        onRetry={() => void router.invalidate()}
      />
    </main>
  )
}
