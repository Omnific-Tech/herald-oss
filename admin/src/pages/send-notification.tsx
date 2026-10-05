import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from '@tanstack/react-router'
import { useState, type ChangeEvent, type FormEvent } from 'react'
import { Field, FieldLayout } from '@/components/field'
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
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { QueryError } from '@/components/query-error'
import { Skeleton } from '@/components/ui/skeleton'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { api, ApiError, type NewMessage, type PublishResult } from '@/lib/api'
import { applicationsQuery, historyQuery, routingQuery } from '@/lib/queries'

type Draft = {
  channel: string
  topic: string
  priority: NewMessage['priority']
  title: string
  body: string
  link: string
  data: string
  collapse_key: string
  expires_in_minutes: string
}

const fieldNames = ['channel', 'topic', 'priority', 'title', 'body', 'link', 'data', 'collapse_key', 'expires_at']

const limits = { title: 200, body: 2000, link: 500, collapse_key: 200 }

function Count({ value, max }: { value: string; max: number }) {
  return (
    <span className="text-xs text-muted-foreground tabular-nums">
      {value.length}/{max}
    </span>
  )
}

const newIdempotencyKey = () => `admin-${crypto.randomUUID()}`

function parseData(value: string): Record<string, unknown> | undefined {
  if (value.trim() === '') return {}
  try {
    const data = JSON.parse(value)
    return data && typeof data === 'object' && !Array.isArray(data) ? data : undefined
  } catch {
    return undefined
  }
}

function resultMessage(result: PublishResult) {
  if (result.deduplicated) {
    return 'Already accepted earlier with this idempotency key. Nothing new was sent.'
  }
  if (result.device_count === 0) {
    return 'Accepted, but no subscribed devices matched. Nothing will be delivered.'
  }
  return `Accepted for ${result.device_count} ${result.device_count === 1 ? 'device' : 'devices'}.`
}

export function SendNotificationPage() {
  const { slug } = useParams({ from: '/app/applications/$slug/notify' })
  const applications = useQuery(applicationsQuery)
  const application = applications.data?.find((candidate) => candidate.slug === slug)
  const routing = useQuery(routingQuery(slug))
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState<Draft>({
    channel: '',
    topic: '',
    priority: 'normal',
    title: '',
    body: '',
    link: '',
    data: '',
    collapse_key: '',
    expires_in_minutes: '10',
  })
  // One key per draft, reused on every retry: if a response is lost after
  // Herald stored the message, the retry is deduplicated instead of sent twice.
  const [idempotencyKey, setIdempotencyKey] = useState(newIdempotencyKey)
  const [dataError, setDataError] = useState<string>()
  const [confirming, setConfirming] = useState(false)

  const publish = useMutation({
    mutationFn: (message: NewMessage) => api.publishMessage(slug, message),
    onSuccess: () => {
      setIdempotencyKey(newIdempotencyKey())
      void queryClient.invalidateQueries({ queryKey: historyQuery(slug).queryKey })
    },
  })

  const serverErrors = publish.error instanceof ApiError ? publish.error.fields : {}
  const errors: Record<string, string | undefined> = { ...serverErrors, data: dataError ?? serverErrors.data }
  const otherErrors = Object.entries(serverErrors)
    .filter(([name]) => !fieldNames.includes(name))
    .map(([, message]) => message)
  const hasFieldError = fieldNames.some((name) => serverErrors[name])
  const formError = !publish.error
    ? undefined
    : otherErrors.length > 0
      ? otherErrors.join(' ')
      : hasFieldError
        ? undefined
        : publish.error.message
  const channels = routing.data?.channels ?? []
  const channel = draft.channel || channels[0]?.id || ''
  const topics = (routing.data?.topics ?? [])
    .filter((topic) => topic.channel === channel && topic.archived_at === null)
    .sort((a, b) => a.label.localeCompare(b.label))
  const channelLabel = channels.find((candidate) => candidate.id === channel)?.label ?? channel
  const topicLabel = topics.find((topic) => topic.id === draft.topic)?.label ?? draft.topic

  function update(name: keyof Draft) {
    return (event: ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) =>
      setDraft((current) => ({ ...current, [name]: event.target.value }))
  }

  function selectChannel(event: ChangeEvent<HTMLSelectElement>) {
    setDraft((current) => ({ ...current, channel: event.target.value, topic: '' }))
  }

  function review(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!parseData(draft.data)) {
      setDataError('must be a JSON object')
      return
    }
    setDataError(undefined)
    setConfirming(true)
  }

  function send() {
    publish.mutate({
      idempotency_key: idempotencyKey,
      channel,
      topic: draft.topic,
      priority: draft.priority,
      title: draft.title,
      body: draft.body,
      link: draft.link,
      data: parseData(draft.data) ?? {},
      collapse_key: draft.collapse_key,
      expires_at: new Date(Date.now() + Number(draft.expires_in_minutes) * 60_000).toISOString(),
    })
  }

  return (
    <div className="grid max-w-2xl gap-6">
      <div>
        <h1 className="text-xl font-semibold">Send notification</h1>
        <p className="text-sm text-muted-foreground">
          To subscribed devices of{' '}
          <Link to="/applications/$slug" params={{ slug }} className="underline underline-offset-4">
            {application?.display_name ?? slug}
          </Link>
        </p>
      </div>
      {routing.isPending ? (
        <Skeleton className="h-96 w-full" />
      ) : routing.isError ? (
        <QueryError error={routing.error} onRetry={() => void routing.refetch()} />
      ) : channels.length === 0 ? (
        <p className="text-sm text-muted-foreground">This application has no channels to send to.</p>
      ) : (
        <Card>
          <CardContent>
            <form className="grid gap-4" onSubmit={review}>
              <div className="grid items-start gap-4 sm:grid-cols-2">
                <FieldLayout
                  id="channel"
                  label="Channel"
                  hint="Routing category devices subscribe to."
                  error={errors.channel}
                >
                  {(control) => (
                    <NativeSelect {...control} className="w-full" value={channel} onChange={selectChannel}>
                      {channels.map((candidate) => (
                        <NativeSelectOption key={candidate.id} value={candidate.id}>
                          {candidate.label}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  )}
                </FieldLayout>
                <FieldLayout
                  id="topic"
                  label="Topic"
                  hint={
                    <>
                      Whole channel reaches channel-wide subscribers; a topic also reaches its exact subscribers.{' '}
                      <Link to="/applications/$slug" params={{ slug }} className="underline underline-offset-4">
                        Manage topics
                      </Link>
                    </>
                  }
                  error={errors.topic}
                >
                  {(control) => (
                    <NativeSelect {...control} className="w-full" value={draft.topic} onChange={update('topic')}>
                      <NativeSelectOption value="">Whole channel</NativeSelectOption>
                      {topics.map((topic) => (
                        <NativeSelectOption key={topic.id} value={topic.id}>
                          {topic.label} ({topic.id})
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  )}
                </FieldLayout>
              </div>
              <FieldLayout id="priority" label="Priority" error={errors.priority}>
                {(control) => (
                  <NativeSelect {...control} value={draft.priority} onChange={update('priority')}>
                    <NativeSelectOption value="normal">normal: may be delayed while a device saves power</NativeSelectOption>
                    <NativeSelectOption value="high">high: delivered immediately where possible</NativeSelectOption>
                  </NativeSelect>
                )}
              </FieldLayout>
              <Field
                id="title"
                label="Title"
                aside={<Count value={draft.title} max={limits.title} />}
                required
                maxLength={limits.title}
                value={draft.title}
                error={errors.title}
                onChange={update('title')}
              />
              <FieldLayout
                id="body"
                label="Body"
                aside={<Count value={draft.body} max={limits.body} />}
                error={errors.body}
              >
                {(control) => (
                  <Textarea
                    {...control}
                    className="max-h-60"
                    required
                    maxLength={limits.body}
                    value={draft.body}
                    onChange={update('body')}
                  />
                )}
              </FieldLayout>
              <Field
                id="link"
                label="Link"
                placeholder="/events/123"
                hint="Opened on tap. An app-relative path; absolute URLs are rejected."
                maxLength={limits.link}
                value={draft.link}
                error={errors.link}
                onChange={update('link')}
              />
              <FieldLayout
                id="data"
                label="Data (JSON)"
                hint="Optional machine-readable JSON for the app. Leave empty to send {}."
                error={errors.data}
              >
                {(control) => (
                  <Textarea
                    {...control}
                    className="max-h-60 font-mono"
                    placeholder='{"event_id": "123"}'
                    value={draft.data}
                    onChange={update('data')}
                  />
                )}
              </FieldLayout>
              <div className="grid items-start gap-4 sm:grid-cols-2">
                <Field
                  id="collapse_key"
                  label="Collapse key"
                  placeholder="earthquake-event-123"
                  hint="Replaces older pending notifications with the same key. Leave blank when every notification matters."
                  maxLength={limits.collapse_key}
                  value={draft.collapse_key}
                  error={errors.collapse_key}
                  onChange={update('collapse_key')}
                />
                <Field
                  id="expires_in_minutes"
                  label="Expires in (minutes)"
                  hint="After expiry Herald neither sends nor displays it. 10 minutes suits urgent alerts."
                  type="number"
                  min={1}
                  required
                  value={draft.expires_in_minutes}
                  error={errors.expires_at}
                  onChange={update('expires_in_minutes')}
                />
              </div>
              {publish.data && (
                <Alert role="status">
                  <AlertDescription>
                    <p>{resultMessage(publish.data)}</p>
                    <p className="font-mono text-xs">Message ID {publish.data.message_id}</p>
                  </AlertDescription>
                </Alert>
              )}
              {formError && (
                <Alert variant="destructive">
                  <AlertDescription>{formError}</AlertDescription>
                </Alert>
              )}
              <Button type="submit" className="justify-self-start" disabled={publish.isPending}>
                {publish.isPending ? 'Sending…' : 'Review and send'}
              </Button>
            </form>
          </CardContent>
        </Card>
      )}
      <AlertDialog open={confirming} onOpenChange={setConfirming}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Send this notification?</AlertDialogTitle>
            <AlertDialogDescription>
              Every device of {application?.display_name ?? slug} subscribed to {channelLabel}
              {draft.topic ? ` (the whole channel or ${topicLabel})` : ''} will receive this{' '}
              {draft.priority}-priority notification. It cannot be recalled.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={send}>Send notification</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
