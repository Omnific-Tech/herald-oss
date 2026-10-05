import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState, type ChangeEvent, type FormEvent } from 'react'
import { Field } from '@/components/field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { api, ApiError, type NewApplication } from '@/lib/api'
import { applicationsQuery } from '@/lib/queries'

const fieldNames = ['slug', 'display_name', 'origin', 'vapid_subject'] as const

function fieldErrors(error: Error | null): Record<string, string> {
  if (!(error instanceof ApiError)) return {}
  if (error.code === 'slug_taken') return { slug: 'An application with this slug already exists.' }
  return error.fields
}

export function NewApplicationPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<NewApplication>({
    slug: '',
    display_name: '',
    origin: '',
    vapid_subject: '',
  })

  const create = useMutation({
    mutationFn: api.createApplication,
    onSuccess: async ({ application }) => {
      await queryClient.invalidateQueries({ queryKey: applicationsQuery.queryKey, exact: true })
      void navigate({ to: '/applications/$slug', params: { slug: application.slug } })
    },
  })

  const errors = fieldErrors(create.error)
  const hasFieldError = fieldNames.some((name) => errors[name])
  const formError = create.error && !hasFieldError ? create.error.message : undefined

  function update(name: keyof NewApplication) {
    return (event: ChangeEvent<HTMLInputElement>) =>
      setForm((current) => ({ ...current, [name]: event.target.value }))
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    create.mutate(form)
  }

  return (
    <div className="grid max-w-xl gap-6">
      <h1 className="text-xl font-semibold">New application</h1>
      <Card>
        <CardContent>
          <form className="grid gap-4" onSubmit={submit}>
            {formError && (
              <Alert variant="destructive">
                <AlertDescription>{formError}</AlertDescription>
              </Alert>
            )}
            <Field
              id="slug"
              label="Slug"
              hint="Lowercase letters, numbers, and single hyphens. Cannot be changed."
              required
              value={form.slug}
              error={errors.slug}
              onChange={update('slug')}
            />
            <Field
              id="display_name"
              label="Display name"
              required
              value={form.display_name}
              error={errors.display_name}
              onChange={update('display_name')}
            />
            <Field
              id="origin"
              label="Origin"
              placeholder="https://app.example"
              required
              value={form.origin}
              error={errors.origin}
              onChange={update('origin')}
            />
            <Field
              id="vapid_subject"
              label="VAPID subject"
              placeholder="mailto:ops@app.example"
              required
              value={form.vapid_subject}
              error={errors.vapid_subject}
              onChange={update('vapid_subject')}
            />
            <Button type="submit" className="justify-self-start" disabled={create.isPending}>
              Create application
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
