const baseUrl = import.meta.env.VITE_ADMIN_API_URL.replace(/\/$/, '')

export type Session = { username: string; expires_at: string }

export type Health = {
  status: 'ok' | 'degraded'
  service: string
  version: string
  database: string
}

export type Application = {
  id: string
  slug: string
  display_name: string
  origin: string
  created_at: string
}

export type NewApplication = {
  slug: string
  display_name: string
  origin: string
  vapid_subject: string
}

export type Producer = {
  id: string
  name: string
  created_at: string
  revoked_at: string | null
}

export type Channel = { id: string; label: string }

export type Topic = {
  id: string
  label: string
  channel: string
  archived_at: string | null
}

export type Routing = { channels: Channel[]; topics: Topic[] }

export type NewTopic = { id: string; label: string; channel: string }

export type NewMessage = {
  idempotency_key: string
  channel: string
  topic: string
  priority: 'normal' | 'high'
  title: string
  body: string
  link: string
  data: Record<string, unknown>
  collapse_key: string
  expires_at: string
}

export type PublishResult = {
  message_id: string
  deduplicated: boolean
  device_count: number
}

export type DeliveryCounts = {
  pending: number
  sent: number
  displayed: number
  delivered: number
  suppressed: number
  failed: number
  expired: number
  superseded: number
}

export type DeliveryError = { state: string; reason: string; count: number }

export type HistoryMessage = {
  id: string
  status: 'published'
  channel: string
  topic: string
  priority: 'normal' | 'high'
  title: string
  body: string
  link: string
  device_count: number
  publisher: { type: 'admin' | 'producer'; name: string }
  delivery_counts: DeliveryCounts
  delivery_errors: DeliveryError[]
  created_at: string
  expires_at: string
}

export type HistoryPage = { messages: HistoryMessage[]; next_cursor: string | null }

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: Record<string, string>
  readonly retryAfter?: number

  constructor(
    status: number,
    code: string,
    message: string,
    fields: Record<string, string> = {},
    retryAfter?: number,
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.fields = fields
    this.retryAfter = retryAfter
  }
}

export function isAuthError(error: unknown): error is ApiError {
  return error instanceof ApiError && (error.status === 401 || error.status === 403)
}

async function request<T>(
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE',
  path: string,
  body?: unknown,
  acceptedStatuses: number[] = [],
): Promise<T> {
  let response: Response
  try {
    response = await fetch(`${baseUrl}${path}`, {
      method,
      credentials: 'include',
      cache: 'no-store',
      headers: method === 'GET' ? undefined : { 'Content-Type': 'application/json' },
      body: method === 'GET' ? undefined : JSON.stringify(body ?? {}),
    })
  } catch {
    throw new ApiError(0, 'network', 'Cannot reach the Herald API.')
  }

  if (response.status === 204) return undefined as T

  const payload = await response.json().catch(() => null)
  if (response.ok || acceptedStatuses.includes(response.status)) {
    if (payload === null) {
      throw new ApiError(response.status, 'bad_response', 'The Herald API returned an unreadable response.')
    }
    return payload as T
  }

  const retryAfter = Number(response.headers.get('Retry-After')) || undefined
  throw new ApiError(
    response.status,
    payload?.code ?? 'http_error',
    payload?.error ?? `Request failed with status ${response.status}.`,
    payload?.fields,
    retryAfter,
  )
}

const applicationPath = (slug: string) => `/applications/${encodeURIComponent(slug)}`
const producersPath = (slug: string) => `${applicationPath(slug)}/producers`
const topicPath = (slug: string, id: string) => `${applicationPath(slug)}/topics/${encodeURIComponent(id)}`

export const api = {
  session: () => request<Session>('GET', '/session'),
  signIn: (credentials: { username: string; password: string }) =>
    request<Session>('POST', '/session', credentials),
  signOut: () => request<void>('DELETE', '/session'),
  health: () => request<Health>('GET', '/health', undefined, [503]),
  applications: async () =>
    (await request<{ applications: Application[] }>('GET', '/applications')).applications,
  createApplication: (input: NewApplication) =>
    request<{ application: Application; vapid_public_key: string }>('POST', '/applications', input),
  producers: async (slug: string) =>
    (await request<{ producers: Producer[] }>('GET', producersPath(slug))).producers,
  createProducer: (slug: string, name: string) =>
    request<{ producer: Producer; token: string }>('POST', producersPath(slug), { name }),
  revokeProducer: (slug: string, id: string) =>
    request<{ producer: Producer }>('POST', `${producersPath(slug)}/${encodeURIComponent(id)}/revoke`),
  publishMessage: (slug: string, message: NewMessage) =>
    request<PublishResult>('POST', `${applicationPath(slug)}/messages`, message),
  messages: (slug: string, cursor: string | null) =>
    request<HistoryPage>(
      'GET',
      `${applicationPath(slug)}/messages?limit=20${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`,
    ),
  routing: (slug: string) => request<Routing>('GET', `${applicationPath(slug)}/routing`),
  createTopic: (slug: string, topic: NewTopic) =>
    request<{ topic: Topic }>('POST', `${applicationPath(slug)}/topics`, topic),
  updateTopic: (slug: string, id: string, label: string) =>
    request<{ topic: Topic }>('PATCH', topicPath(slug, id), { label }),
  archiveTopic: (slug: string, id: string) =>
    request<{ topic: Topic }>('POST', `${topicPath(slug, id)}/archive`),
}
