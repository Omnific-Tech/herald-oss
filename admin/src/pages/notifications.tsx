import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { useState } from 'react'
import { QueryError } from '@/components/query-error'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import type { DeliveryCounts, DeliveryError, HistoryMessage } from '@/lib/api'
import { applicationsQuery, historyQuery } from '@/lib/queries'
import { formatTimestamp } from '@/lib/utils'

const deliveryLabels: [keyof DeliveryCounts, string][] = [
  ['pending', 'Pending'],
  ['sent', 'Sent'],
  ['displayed', 'Displayed'],
  ['delivered', 'Delivered'],
  ['suppressed', 'Suppressed'],
  ['failed', 'Failed'],
  ['expired', 'Expired'],
  ['superseded', 'Superseded'],
]

const errorStates: Record<string, { label: string; variant: 'secondary' | 'destructive' }> = {
  pending: { label: 'Retrying', variant: 'secondary' },
  failed: { label: 'Failed', variant: 'destructive' },
}

export function NotificationsPage() {
  const { app } = useSearch({ from: '/app/notifications' })
  const navigate = useNavigate()
  const applications = useQuery(applicationsQuery)
  const slug = app ?? applications.data?.[0]?.slug

  return (
    <div className="grid gap-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold">Notifications</h1>
          <p className="max-w-2xl text-sm text-muted-foreground">
            Sent means FCM or Web Push accepted the send; displayed means the device confirmed it showed the
            notification. Counts can be below the targeted devices when a device has been deleted. Retrying
            errors are still pending; failed is final.
          </p>
        </div>
        {applications.data && applications.data.length > 0 && (
          <div className="grid gap-1.5">
            <Label htmlFor="application">Application</Label>
            <NativeSelect
              id="application"
              value={slug}
              onChange={(event) => void navigate({ to: '/notifications', search: { app: event.target.value } })}
            >
              {applications.data.map((application) => (
                <NativeSelectOption key={application.slug} value={application.slug}>
                  {application.display_name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
        )}
      </div>
      {applications.isPending ? (
        <Skeleton className="h-48 w-full" />
      ) : applications.isError ? (
        <QueryError error={applications.error} onRetry={() => void applications.refetch()} />
      ) : slug ? (
        <History key={slug} slug={slug} />
      ) : (
        <p className="text-sm text-muted-foreground">No applications yet.</p>
      )}
    </div>
  )
}

function History({ slug }: { slug: string }) {
  const history = useInfiniteQuery(historyQuery(slug))
  const [now] = useState(Date.now)

  if (history.isPending) return <Skeleton className="h-48 w-full" />
  if (!history.data) {
    return <QueryError error={history.error ?? new Error('Unexpected error.')} onRetry={() => void history.refetch()} />
  }

  const messages = history.data.pages.flatMap((page) => page.messages)
  if (messages.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No notifications sent yet.{' '}
        <Link to="/applications/$slug/notify" params={{ slug }} className="underline underline-offset-4">
          Send one
        </Link>
      </p>
    )
  }

  return (
    <div className="grid gap-3">
      {messages.map((message) => (
        <MessageItem key={message.id} message={message} now={now} />
      ))}
      {history.isFetchNextPageError && (
        <Alert variant="destructive">
          <AlertDescription>{history.error?.message}</AlertDescription>
        </Alert>
      )}
      {history.hasNextPage && (
        <Button
          variant="outline"
          className="justify-self-center"
          disabled={history.isFetchingNextPage}
          onClick={() => void history.fetchNextPage()}
        >
          {history.isFetchingNextPage ? 'Loading…' : 'Load more'}
        </Button>
      )}
    </div>
  )
}

function MessageItem({ message, now }: { message: HistoryMessage; now: number }) {
  const expired = Date.parse(message.expires_at) <= now
  const deliveries = deliveryLabels.filter(([state]) => message.delivery_counts[state] > 0)

  return (
    <article
      aria-labelledby={`message-${message.id}`}
      className="grid gap-3 rounded-xl bg-card p-4 text-sm ring-1 ring-foreground/10"
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <h2 id={`message-${message.id}`} className="font-medium">
          {message.title}
        </h2>
        <div className="flex gap-1.5">
          <Badge variant={message.priority === 'high' ? 'destructive' : 'secondary'}>{message.priority}</Badge>
          <Badge variant="outline">{message.status}</Badge>
        </div>
      </div>
      <p className="whitespace-pre-wrap">{message.body}</p>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">
        <span className="font-mono">
          {message.channel} / {message.topic || 'whole channel'}
        </span>
        {message.link && <span className="font-mono">{message.link}</span>}
        <span>
          Published by {message.publisher.name} ({message.publisher.type})
        </span>
        <span>{formatTimestamp(message.created_at)}</span>
        <span>
          {expired ? 'Expired' : 'Expires'} {formatTimestamp(message.expires_at)}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t pt-3">
        <span className="font-medium">
          {message.device_count} {message.device_count === 1 ? 'device' : 'devices'} targeted
        </span>
        {deliveries.length === 0 ? (
          <span className="text-muted-foreground">No delivery records</span>
        ) : (
          deliveries.map(([state, label]) => (
            <Badge key={state} variant="outline">
              {label} {message.delivery_counts[state]}
            </Badge>
          ))
        )}
      </div>
      {message.delivery_errors.length > 0 && <DeliveryErrors id={message.id} errors={message.delivery_errors} />}
    </article>
  )
}

function DeliveryErrors({ id, errors }: { id: string; errors: DeliveryError[] }) {
  return (
    <div className="grid gap-2">
      <h3 id={`errors-${id}`} className="font-medium">
        Delivery errors
      </h3>
      <ul aria-labelledby={`errors-${id}`} className="grid gap-1.5">
        {errors.map((error) => {
          const state = errorStates[error.state] ?? { label: error.state, variant: 'secondary' }
          return (
            <li key={`${error.state}:${error.reason}`} className="flex flex-wrap items-center gap-2">
              <Badge variant={state.variant}>{state.label}</Badge>
              <span className="font-mono break-all">{error.reason}</span>
              <span className="text-muted-foreground">
                {error.count} {error.count === 1 ? 'device' : 'devices'}
              </span>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
