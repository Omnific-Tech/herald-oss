import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { QueryError } from '@/components/query-error'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { applicationsQuery } from '@/lib/queries'
import { formatTimestamp } from '@/lib/utils'

export function ApplicationsPage() {
  const applications = useQuery(applicationsQuery)

  return (
    <div className="grid gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Applications</h1>
        <Button asChild>
          <Link to="/applications/new">New application</Link>
        </Button>
      </div>
      <Card>
        <CardContent>
          {applications.isPending ? (
            <Skeleton className="h-32 w-full" />
          ) : applications.isError ? (
            <QueryError error={applications.error} onRetry={() => void applications.refetch()} />
          ) : applications.data.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">No applications yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Slug</TableHead>
                  <TableHead>Name</TableHead>
                  <TableHead>Origin</TableHead>
                  <TableHead>Created</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {applications.data.map((application) => (
                  <TableRow key={application.id}>
                    <TableCell>
                      <Link
                        to="/applications/$slug"
                        params={{ slug: application.slug }}
                        className="font-mono underline-offset-4 hover:underline"
                      >
                        {application.slug}
                      </Link>
                    </TableCell>
                    <TableCell>{application.display_name}</TableCell>
                    <TableCell>{application.origin}</TableCell>
                    <TableCell>{formatTimestamp(application.created_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
