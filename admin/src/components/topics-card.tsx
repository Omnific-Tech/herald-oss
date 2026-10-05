import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { Field, FieldLayout } from '@/components/field'
import { QueryError } from '@/components/query-error'
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
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { api, ApiError, type Channel, type Routing, type Topic } from '@/lib/api'
import { routingQuery } from '@/lib/queries'

export function TopicsCard({ slug }: { slug: string }) {
  const routing = useQuery(routingQuery(slug))

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h2>Topics</h2>
        </CardTitle>
      </CardHeader>
      <CardContent className="grid gap-6">
        {routing.isPending ? (
          <Skeleton className="h-24 w-full" />
        ) : routing.isError ? (
          <QueryError error={routing.error} onRetry={() => void routing.refetch()} />
        ) : (
          <>
            <CreateTopic slug={slug} channels={routing.data.channels} />
            <TopicTable slug={slug} routing={routing.data} />
          </>
        )}
      </CardContent>
    </Card>
  )
}

function CreateTopic({ slug, channels }: { slug: string; channels: Channel[] }) {
  const queryClient = useQueryClient()
  const [id, setId] = useState('')
  const [label, setLabel] = useState('')
  const [channel, setChannel] = useState(channels[0]?.id ?? '')

  const create = useMutation({
    mutationFn: () => api.createTopic(slug, { id, label, channel }),
    onSuccess: () => {
      setId('')
      setLabel('')
      void queryClient.invalidateQueries({ queryKey: routingQuery(slug).queryKey })
    },
  })

  const error = create.error
  const fields: Record<string, string | undefined> =
    error instanceof ApiError
      ? error.code === 'topic_exists'
        ? { id: 'A topic with this ID already exists.' }
        : error.fields
      : {}
  const formError = error && !fields.id && !fields.label && !fields.channel ? error.message : undefined

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    create.mutate()
  }

  return (
    <form className="grid gap-4" onSubmit={submit}>
      {formError && (
        <Alert variant="destructive">
          <AlertDescription>{formError}</AlertDescription>
        </Alert>
      )}
      <div className="grid items-start gap-4 sm:grid-cols-3">
        <Field
          id="topic-id"
          label="Topic ID"
          placeholder="earthquake.jakarta"
          hint="Permanent. Cannot be changed or reused."
          required
          value={id}
          error={fields.id}
          onChange={(event) => setId(event.target.value)}
        />
        <Field
          id="topic-label"
          label="Label"
          placeholder="Jakarta earthquakes"
          required
          value={label}
          error={fields.label}
          onChange={(event) => setLabel(event.target.value)}
        />
        <FieldLayout id="topic-channel" label="Channel" error={fields.channel}>
          {(control) => (
            <NativeSelect {...control} className="w-full" value={channel} onChange={(event) => setChannel(event.target.value)}>
              {channels.map((candidate) => (
                <NativeSelectOption key={candidate.id} value={candidate.id}>
                  {candidate.label}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          )}
        </FieldLayout>
      </div>
      <Button type="submit" className="justify-self-start" disabled={create.isPending}>
        Add topic
      </Button>
    </form>
  )
}

function TopicTable({ slug, routing }: { slug: string; routing: Routing }) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<{ id: string; label: string } | null>(null)
  const [archiveTarget, setArchiveTarget] = useState<Topic | null>(null)
  const refresh = () => queryClient.invalidateQueries({ queryKey: routingQuery(slug).queryKey })

  const update = useMutation({
    mutationFn: ({ id, label }: { id: string; label: string }) => api.updateTopic(slug, id, label),
    onSuccess: async () => {
      await refresh()
      setEditing(null)
    },
  })

  const archive = useMutation({
    mutationFn: (topic: Topic) => api.archiveTopic(slug, topic.id),
    onSettled: refresh,
  })

  const channelLabel = (id: string) => routing.channels.find((channel) => channel.id === id)?.label ?? id
  const topics = [...routing.topics].sort(
    (a, b) => a.channel.localeCompare(b.channel) || a.id.localeCompare(b.id),
  )

  if (topics.length === 0) {
    return <p className="py-6 text-center text-sm text-muted-foreground">No topics yet.</p>
  }

  return (
    <div className="grid gap-4">
      {update.error && (
        <Alert variant="destructive">
          <AlertDescription>{update.error.message}</AlertDescription>
        </Alert>
      )}
      {archive.error && (
        <Alert variant="destructive">
          <AlertDescription>{archive.error.message}</AlertDescription>
        </Alert>
      )}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>ID</TableHead>
            <TableHead>Label</TableHead>
            <TableHead>Channel</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {topics.map((topic) => {
            const isEditing = editing?.id === topic.id
            return (
              <TableRow key={topic.id}>
                <TableCell className="font-mono">{topic.id}</TableCell>
                <TableCell>
                  {isEditing ? (
                    <Input
                      aria-label={`Label for ${topic.id}`}
                      value={editing.label}
                      onChange={(event) => setEditing({ id: topic.id, label: event.target.value })}
                    />
                  ) : (
                    topic.label
                  )}
                </TableCell>
                <TableCell>{channelLabel(topic.channel)}</TableCell>
                <TableCell>
                  {topic.archived_at ? (
                    <Badge variant="outline">Archived</Badge>
                  ) : (
                    <Badge variant="secondary">Active</Badge>
                  )}
                </TableCell>
                <TableCell className="text-right">
                  {isEditing ? (
                    <div className="flex justify-end gap-2">
                      <Button size="sm" disabled={update.isPending} onClick={() => update.mutate(editing)}>
                        Save
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => setEditing(null)}>
                        Cancel
                      </Button>
                    </div>
                  ) : (
                    !topic.archived_at && (
                      <div className="flex justify-end gap-2">
                        <Button
                          size="sm"
                          variant="outline"
                          aria-label={`Edit ${topic.id}`}
                          onClick={() => {
                            update.reset()
                            setEditing({ id: topic.id, label: topic.label })
                          }}
                        >
                          Edit
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          aria-label={`Archive ${topic.id}`}
                          onClick={() => setArchiveTarget(topic)}
                        >
                          Archive
                        </Button>
                      </div>
                    )
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      <AlertDialog open={archiveTarget !== null} onOpenChange={(open) => !open && setArchiveTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Archive {archiveTarget?.id}?</AlertDialogTitle>
            <AlertDialogDescription>
              It will no longer be offered when sending notifications. It stays listed here, and its ID
              cannot be reused.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => archiveTarget && archive.mutate(archiveTarget)}>
              Archive topic
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
