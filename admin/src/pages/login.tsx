import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'
import { Field } from '@/components/field'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { api, ApiError } from '@/lib/api'
import { sessionQuery } from '@/lib/queries'

export type LoginReason = 'expired' | 'forbidden' | 'signed-out'

const reasonMessages: Record<LoginReason, string> = {
  expired: 'Your session expired. Sign in again.',
  forbidden: 'You are not authorized for that action. Sign in again.',
  'signed-out': 'Signed out.',
}

function signInErrorMessage(error: Error) {
  if (!(error instanceof ApiError)) return error.message
  if (error.status === 401) return 'Invalid username or password.'
  if (error.status === 429) {
    return error.retryAfter
      ? `Too many sign-in attempts. Try again in ${error.retryAfter} seconds.`
      : 'Too many sign-in attempts. Try again later.'
  }
  return error.message
}

export function LoginPage() {
  const { reason } = useSearch({ from: '/login' })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')

  const signIn = useMutation({
    mutationFn: api.signIn,
    meta: { authRedirect: false },
    onSuccess: (session) => {
      queryClient.setQueryData(sessionQuery.queryKey, session)
      void navigate({ to: '/' })
    },
    onError: () => setPassword(''),
  })

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    signIn.mutate({ username, password })
  }

  return (
    <main className="flex min-h-svh items-center justify-center bg-muted/40 p-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>
            <h1 className="text-lg">Sign in to Herald</h1>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <form className="grid gap-4" onSubmit={submit}>
            {reason && !signIn.error && (
              <Alert role="status">
                <AlertDescription>{reasonMessages[reason]}</AlertDescription>
              </Alert>
            )}
            {signIn.error && (
              <Alert variant="destructive">
                <AlertDescription>{signInErrorMessage(signIn.error)}</AlertDescription>
              </Alert>
            )}
            <Field
              id="username"
              label="Username"
              autoComplete="username"
              required
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
            <Field
              id="password"
              label="Password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
            <Button type="submit" disabled={signIn.isPending}>
              Sign in
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
