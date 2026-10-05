import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'
import { Field } from '@/components/field'
import { ProducerToken } from '@/components/producer-token'
import { QueryError } from '@/components/query-error'
import { TopicsCard } from '@/components/topics-card'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { api, ApiError, type Producer } from '@/lib/api'
import { applicationsQuery, producersQuery } from '@/lib/queries'
import { formatTimestamp } from '@/lib/utils'

export function ApplicationPage() {
  const { slug } = useParams({ from: '/app/applications/$slug' })
  const applications = useQuery(applicationsQuery)
  const producers = useQuery(producersQuery(slug))
  const application = applications.data?.find((candidate) => candidate.slug === slug)
  const notFound = producers.error instanceof ApiError && producers.error.status === 404

  return (
    <div className="grid gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold">{application?.display_name ?? slug}</h1>
          {application && (
            <p className="text-sm text-muted-foreground">
              <span className="font-mono">{application.slug}</span> · {application.origin}
            </p>
          )}
        </div>
        {!notFound && (
          <div className="flex gap-2">
            <Button variant="outline" asChild>
              <Link to="/notifications" search={{ app: slug }}>
                Notification history
              </Link>
            </Button>
            <Button asChild>
              <Link to="/applications/$slug/notify" params={{ slug }}>
                Send notification
              </Link>
            </Button>
          </div>
        )}
      </div>
      {notFound ? (
        <p className="text-sm text-muted-foreground">Application not found.</p>
      ) : (
        <>
          <CreateProducer slug={slug} />
          <Card>
            <CardHeader>
              <CardTitle>
                <h2>Producers</h2>
              </CardTitle>
            </CardHeader>
            <CardContent>
              {producers.isPending ? (
                <Skeleton className="h-24 w-full" />
              ) : producers.isError ? (
                <QueryError error={producers.error} onRetry={() => void producers.refetch()} />
              ) : (
                <ProducerTable slug={slug} producers={producers.data} />
              )}
            </CardContent>
          </Card>
          <TopicsCard slug={slug} />
        </>
      )}
    </div>
  )
}

function CreateProducer({ slug }: { slug: string }) {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')

  // The token lives only in this mutation's state. gcTime 0 drops it from the
  // mutation cache as soon as this component unmounts; reset() drops it on dismiss.
  const create = useMutation({
    mutationFn: (producerName: string) => api.createProducer(slug, producerName),
    gcTime: 0,
    onSuccess: () => {
      setName('')
      void queryClient.invalidateQueries({ queryKey: producersQuery(slug).queryKey })
    },
  })

  const error = create.error
  const nameError =
    error instanceof ApiError
      ? error.code === 'name_taken'
        ? 'An active producer with this name already exists.'
        : error.fields.name
      : undefined

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    create.mutate(name)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Create producer</h2>
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-4">
        {create.data && (
          <ProducerToken
            name={create.data.producer.name}
            token={create.data.token}
            onDismiss={() => create.reset()}
          />
        )}
        {error && !nameError && (
          <Alert variant="destructive">
            <AlertDescription>{error.message}</AlertDescription>
          </Alert>
        )}
        <form className="flex flex-wrap items-end gap-3" onSubmit={submit}>
          <div className="min-w-64 flex-1">
            <Field
              id="producer-name"
              label="Producer name"
              placeholder="app-api"
              required
              value={name}
              error={nameError}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <Button type="submit" disabled={create.isPending}>
            Create producer
          </Button>
        </form>
      </CardContent>
    </Card>
  )
}

function ProducerTable({ slug, producers }: { slug: string; producers: Producer[] }) {
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<Producer | null>(null)

  const revoke = useMutation({
    mutationFn: (producer: Producer) => api.revokeProducer(slug, producer.id),
    onSettled: () => queryClient.invalidateQueries({ queryKey: producersQuery(slug).queryKey }),
  })

  if (producers.length === 0) {
    return <p className="py-6 text-center text-sm text-muted-foreground">No producers yet.</p>
  }

  return (
    <div className="grid gap-4">
      {revoke.error && (
        <Alert variant="destructive">
          <AlertDescription>{revoke.error.message}</AlertDescription>
        </Alert>
      )}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Revoked</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {producers.map((producer) => (
            <TableRow key={producer.id}>
              <TableCell className="font-mono">{producer.name}</TableCell>
              <TableCell>
                {producer.revoked_at ? (
                  <Badge variant="outline">Revoked</Badge>
                ) : (
                  <Badge variant="secondary">Active</Badge>
                )}
              </TableCell>
              <TableCell>{formatTimestamp(producer.created_at)}</TableCell>
              <TableCell>{producer.revoked_at ? formatTimestamp(producer.revoked_at) : '—'}</TableCell>
              <TableCell className="text-right">
                {!producer.revoked_at && (
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label={`Revoke ${producer.name}`}
                    onClick={() => setTarget(producer)}
                  >
                    Revoke
                  </Button>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <AlertDialog open={target !== null} onOpenChange={(open) => !open && setTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke {target?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Requests signed with this producer's token will be rejected immediately. This cannot
              be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => target && revoke.mutate(target)}
            >
              Revoke producer
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
