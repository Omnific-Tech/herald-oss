import { useQuery } from '@tanstack/react-query'
import { QueryError } from '@/components/query-error'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { healthQuery } from '@/lib/queries'

export function OverviewPage() {
  const health = useQuery(healthQuery)

  return (
    <div className="grid gap-6">
      <h1 className="text-xl font-semibold">Overview</h1>
      <Card role="region" aria-labelledby="health-title">
        <CardHeader>
          <CardTitle id="health-title">Service health</CardTitle>
        </CardHeader>
        <CardContent>
          {health.isPending ? (
            <Skeleton className="h-24 w-full" />
          ) : health.isError ? (
            <QueryError error={health.error} onRetry={() => void health.refetch()} />
          ) : (
            <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
              <dt className="text-muted-foreground">Status</dt>
              <dd>
                <Badge variant={health.data.status === 'ok' ? 'secondary' : 'destructive'}>
                  {health.data.status}
                </Badge>
              </dd>
              <dt className="text-muted-foreground">Service</dt>
              <dd>{health.data.service}</dd>
              <dt className="text-muted-foreground">Version</dt>
              <dd className="font-mono">{health.data.version}</dd>
              <dt className="text-muted-foreground">Database</dt>
              <dd>{health.data.database}</dd>
            </dl>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
