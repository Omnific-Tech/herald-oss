// Development and E2E stand-in for the Herald admin API (docs/admin-api.md).
// Never deployed: nothing in src/ imports it.
import { randomBytes, randomUUID } from 'node:crypto'
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'

const port = Number(process.env.MOCK_PORT ?? 4011)
const adminOrigin = process.env.ADMIN_ORIGIN ?? 'http://localhost:5173'
const adminUsername = 'admin'
const adminPassword = process.env.MOCK_ADMIN_PASSWORD ?? 'herald-dev'
const cookieName = '__Host-herald_admin'
const sessionTtlMs = 8 * 60 * 60 * 1000
const maxFailedLogins = 5
const retryAfterSeconds = 30

type Application = {
  id: string
  slug: string
  display_name: string
  origin: string
  created_at: string
}

type Producer = {
  id: string
  application_id: string
  name: string
  created_at: string
  revoked_at: string | null
}

type Topic = {
  application_id: string
  id: string
  label: string
  channel: string
  archived_at: string | null
}

const channels = [
  { id: 'alerts', label: 'Alerts' },
  { id: 'system', label: 'System' },
]

type Subscription = { application_id: string; channel: string; topic: string }

const deliveryStates = ['pending', 'sent', 'displayed', 'delivered', 'suppressed', 'failed', 'expired', 'superseded']

type Message = {
  application_id: string
  idempotency_key: string
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
  delivery_counts: Record<string, number>
  delivery_errors: { state: string; reason: string; count: number }[]
  created_at: string
  expires_at: string
}

type Session = { username: string; expires_at: string }

type State = {
  applications: Application[]
  producers: Producer[]
  // One entry per device; each device holds one subscription in the mock.
  devices: Subscription[]
  topics: Topic[]
  messages: Message[]
  sessions: Map<string, Session>
  failedLogins: number
}

let state = seed('default')

function seed(kind: string): State {
  const empty: State = {
    applications: [],
    producers: [],
    devices: [],
    topics: [],
    messages: [],
    sessions: new Map(),
    failedLogins: 0,
  }
  if (kind === 'empty') return empty

  const sample: Application = {
    id: randomUUID(),
    slug: 'sample',
    display_name: 'Sample',
    origin: 'https://sample.example',
    created_at: '2026-09-01T08:00:00Z',
  }
  const other: Application = {
    id: randomUUID(),
    slug: 'other',
    display_name: 'Other',
    origin: 'https://other.example',
    created_at: '2026-09-05T08:00:00Z',
  }
  return {
    ...empty,
    applications: [sample, other],
    producers: [
      {
        id: randomUUID(),
        application_id: sample.id,
        name: 'sample-api',
        created_at: '2026-09-02T09:00:00Z',
        revoked_at: null,
      },
      {
        id: randomUUID(),
        application_id: sample.id,
        name: 'sample-legacy',
        created_at: '2026-09-01T09:00:00Z',
        revoked_at: '2026-09-10T12:00:00Z',
      },
    ],
    devices: [
      { application_id: sample.id, channel: 'alerts', topic: '' },
      { application_id: sample.id, channel: 'alerts', topic: 'btc' },
      { application_id: sample.id, channel: 'system', topic: '' },
    ],
    topics: [
      topic(sample, 'earthquake.jakarta', 'Jakarta earthquakes', 'alerts'),
      topic(sample, 'btc', 'Bitcoin price', 'alerts'),
      topic(sample, 'maintenance', 'Maintenance windows', 'system'),
      { ...topic(sample, 'earthquake.bandung', 'Bandung earthquakes', 'alerts'), archived_at: '2026-09-15T00:00:00Z' },
      topic(other, 'markets.open', 'Market open', 'alerts'),
    ],
    messages: seedHistory(sample),
  }
}

function counts(values: Record<string, number>) {
  return Object.fromEntries(deliveryStates.map((name) => [name, values[name] ?? 0]))
}

function seedHistory(application: Application): Message[] {
  const at = (hoursAgo: number) => new Date(Date.parse('2026-09-20T08:00:00Z') - hoursAgo * 3_600_000).toISOString()
  const message = (hoursAgo: number, fields: Partial<Message>): Message => ({
    application_id: application.id,
    idempotency_key: randomUUID(),
    id: randomUUID(),
    status: 'published',
    channel: 'system',
    topic: '',
    priority: 'normal',
    title: '',
    body: '',
    link: '',
    device_count: 1,
    publisher: { type: 'producer', name: 'sample-api' },
    delivery_counts: counts({ sent: 1 }),
    delivery_errors: [],
    created_at: at(hoursAgo),
    expires_at: new Date(Date.parse(at(hoursAgo)) + 600_000).toISOString(),
    ...fields,
  })
  return [
    message(0, {
      channel: 'alerts',
      topic: 'earthquake.jakarta',
      priority: 'high',
      title: 'Earthquake alert',
      body: 'Magnitude 5.1 near Jakarta',
      link: '/events/123',
      device_count: 3,
      publisher: { type: 'admin', name: 'operator' },
      // Two of three rows remain: one device was deleted after fan-out.
      delivery_counts: counts({ sent: 1, displayed: 1 }),
    }),
    message(1, {
      channel: 'alerts',
      topic: 'btc',
      title: 'Bitcoin crossed 100k',
      body: 'BTC is trading above 100,000 USD.',
      device_count: 2,
      delivery_counts: counts({ delivered: 2 }),
    }),
    message(2, {
      title: 'Scheduled maintenance',
      body: 'Herald will be read-only from 22:00 UTC.',
      device_count: 5,
      delivery_counts: counts({ displayed: 1, pending: 1, failed: 3 }),
      delivery_errors: [
        { state: 'failed', reason: 'FCM 404: Requested entity was not found.', count: 2 },
        { state: 'failed', reason: 'Web Push HTTP 410: subscription gone', count: 1 },
        { state: 'pending', reason: 'FCM transport error', count: 1 },
      ],
    }),
    ...Array.from({ length: 22 }, (_, index) =>
      message(index + 3, { title: `Status update ${index + 4}`, body: 'All systems operational.' }),
    ),
  ]
}

function publicMessage(message: Message) {
  const { application_id: _application, idempotency_key: _key, ...visible } = message
  return visible
}

function topic(application: Application, id: string, label: string, channel: string): Topic {
  return { application_id: application.id, id, label, channel, archived_at: null }
}

function publicTopic(topic: Topic) {
  const { id, label, channel, archived_at } = topic
  return { id, label, channel, archived_at }
}

function validateLabel(body: Record<string, unknown>) {
  const label = text(body, 'label').trim()
  if (label === '' || label.length > 100) {
    throw new HttpError(422, 'invalid_fields', 'topic fields are invalid', {
      label: 'must be between 1 and 100 characters',
    })
  }
  return label
}

function findTopic(application: Application, id: string) {
  const found = state.topics.find((candidate) => candidate.application_id === application.id && candidate.id === id)
  if (!found) throw new HttpError(404, 'topic_not_found', 'topic was not found')
  return found
}

class HttpError extends Error {
  status: number
  code: string
  fields?: Record<string, string>

  constructor(status: number, code: string, message: string, fields?: Record<string, string>) {
    super(message)
    this.status = status
    this.code = code
    this.fields = fields
  }
}

function send(response: ServerResponse, status: number, body?: unknown) {
  if (body === undefined) {
    response.writeHead(status).end()
    return
  }
  response.writeHead(status, { 'Content-Type': 'application/json' }).end(JSON.stringify(body))
}

async function readJson(request: IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: Buffer[] = []
  for await (const chunk of request) chunks.push(chunk as Buffer)
  try {
    const value = JSON.parse(Buffer.concat(chunks).toString() || '{}')
    if (value && typeof value === 'object' && !Array.isArray(value)) return value
  } catch {}
  throw new HttpError(422, 'invalid_fields', 'request fields are invalid', {
    request: 'valid JSON is required',
  })
}

function text(body: Record<string, unknown>, key: string) {
  const value = body[key]
  return typeof value === 'string' ? value : ''
}

function sessionToken(request: IncomingMessage) {
  for (const part of (request.headers.cookie ?? '').split(';')) {
    const [name, ...value] = part.trim().split('=')
    if (name === cookieName) return value.join('=')
  }
  return ''
}

function currentSession(request: IncomingMessage) {
  const token = sessionToken(request)
  const session = state.sessions.get(token)
  if (!session || Date.parse(session.expires_at) <= Date.now()) {
    state.sessions.delete(token)
    throw new HttpError(401, 'unauthenticated', 'admin session is missing or expired')
  }
  return session
}

function setCookie(response: ServerResponse, token: string, maxAgeSeconds: number) {
  response.setHeader(
    'Set-Cookie',
    `${cookieName}=${token}; Path=/; Max-Age=${maxAgeSeconds}; HttpOnly; Secure; SameSite=Strict`,
  )
}

function findApplication(slug: string) {
  const application = state.applications.find((candidate) => candidate.slug === slug)
  if (!application) throw new HttpError(404, 'application_not_found', 'application was not found')
  return application
}

function publicProducer(producer: Producer) {
  const { id, name, created_at, revoked_at } = producer
  return { id, name, created_at, revoked_at }
}

function isHttpsOrigin(value: string) {
  try {
    const url = new URL(value)
    return (
      url.protocol === 'https:' &&
      url.hostname !== '' &&
      !url.username &&
      url.pathname === '/' &&
      !/[?#]/.test(value)
    )
  } catch {
    return false
  }
}

function isVapidSubject(value: string) {
  if (value.startsWith('mailto:')) return value.slice('mailto:'.length).includes('@')
  try {
    const url = new URL(value)
    return url.protocol === 'https:' && url.hostname !== '' && !url.username
  } catch {
    return false
  }
}

async function handleApi(request: IncomingMessage, response: ServerResponse, path: string) {
  const method = request.method ?? 'GET'

  if (method !== 'GET') {
    if (request.headers.origin !== adminOrigin) {
      throw new HttpError(403, 'origin_rejected', 'request origin is not allowed')
    }
    if (!(request.headers['content-type'] ?? '').startsWith('application/json')) {
      throw new HttpError(415, 'unsupported_media_type', 'Content-Type must be application/json')
    }
  }

  if (path === '/api/session' && method === 'POST') {
    if (state.failedLogins >= maxFailedLogins) {
      response.setHeader('Retry-After', String(retryAfterSeconds))
      throw new HttpError(429, 'rate_limited', 'too many login attempts')
    }
    const body = await readJson(request)
    if (text(body, 'username') !== adminUsername || text(body, 'password') !== adminPassword) {
      state.failedLogins++
      throw new HttpError(401, 'invalid_credentials', 'invalid username or password')
    }
    state.failedLogins = 0
    const token = randomBytes(32).toString('base64url')
    const session = {
      username: adminUsername,
      expires_at: new Date(Date.now() + sessionTtlMs).toISOString(),
    }
    state.sessions.set(token, session)
    setCookie(response, token, sessionTtlMs / 1000)
    return send(response, 200, session)
  }

  if (path === '/api/session' && method === 'DELETE') {
    state.sessions.delete(sessionToken(request))
    setCookie(response, '', 0)
    return send(response, 204)
  }

  const session = currentSession(request)

  if (path === '/api/session' && method === 'GET') return send(response, 200, session)

  if (path === '/api/health' && method === 'GET') {
    return send(response, 200, {
      status: 'ok',
      service: 'herald',
      version: 'v1.4.0-mock',
      database: 'ok',
    })
  }

  if (path === '/api/applications' && method === 'GET') {
    const applications = [...state.applications].sort((a, b) => a.slug.localeCompare(b.slug))
    return send(response, 200, { applications })
  }

  if (path === '/api/applications' && method === 'POST') {
    const body = await readJson(request)
    const slug = text(body, 'slug')
    const displayName = text(body, 'display_name')
    const origin = text(body, 'origin')
    const vapidSubject = text(body, 'vapid_subject')

    const fields: Record<string, string> = {}
    if (slug.length > 64 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug)) {
      fields.slug = 'must use lowercase letters, numbers, and single hyphens'
    }
    if (displayName === '' || displayName.length > 200) {
      fields.display_name = 'must be between 1 and 200 characters'
    }
    if (!isHttpsOrigin(origin)) {
      fields.origin = 'must be an HTTPS origin without a path, query, or fragment'
    }
    if (!isVapidSubject(vapidSubject)) {
      fields.vapid_subject = 'must be a mailto: or HTTPS URL'
    }
    if (Object.keys(fields).length > 0) {
      throw new HttpError(422, 'invalid_fields', 'application fields are invalid', fields)
    }
    if (state.applications.some((application) => application.slug === slug)) {
      throw new HttpError(409, 'slug_taken', 'application slug is already in use')
    }

    const application: Application = {
      id: randomUUID(),
      slug,
      display_name: displayName,
      origin,
      created_at: new Date().toISOString(),
    }
    state.applications.push(application)
    return send(response, 201, {
      application,
      vapid_public_key: randomBytes(65).toString('base64url'),
    })
  }

  const producers = path.match(/^\/api\/applications\/([^/]+)\/producers$/)
  if (producers && method === 'GET') {
    const application = findApplication(decodeURIComponent(producers[1]))
    const list = state.producers
      .filter((producer) => producer.application_id === application.id)
      .sort((a, b) => a.created_at.localeCompare(b.created_at))
      .map(publicProducer)
    return send(response, 200, { producers: list })
  }

  if (producers && method === 'POST') {
    const application = findApplication(decodeURIComponent(producers[1]))
    const name = text(await readJson(request), 'name').trim()
    if (name === '' || name.length > 100) {
      throw new HttpError(422, 'invalid_fields', 'producer fields are invalid', {
        name: 'must be between 1 and 100 characters',
      })
    }
    const taken = state.producers.some(
      (producer) =>
        producer.application_id === application.id &&
        producer.name === name &&
        producer.revoked_at === null,
    )
    if (taken) throw new HttpError(409, 'name_taken', 'an active producer already uses this name')

    const producer: Producer = {
      id: randomUUID(),
      application_id: application.id,
      name,
      created_at: new Date().toISOString(),
      revoked_at: null,
    }
    state.producers.push(producer)
    return send(response, 201, {
      producer: publicProducer(producer),
      token: randomBytes(32).toString('base64url'),
    })
  }

  const revoke = path.match(/^\/api\/applications\/([^/]+)\/producers\/([^/]+)\/revoke$/)
  if (revoke && method === 'POST') {
    const application = findApplication(decodeURIComponent(revoke[1]))
    const producer = state.producers.find(
      (candidate) => candidate.id === revoke[2] && candidate.application_id === application.id,
    )
    if (!producer) throw new HttpError(404, 'producer_not_found', 'producer was not found')
    if (producer.revoked_at) throw new HttpError(409, 'already_revoked', 'producer is already revoked')
    producer.revoked_at = new Date().toISOString()
    return send(response, 200, { producer: publicProducer(producer) })
  }

  const messages = path.match(/^\/api\/applications\/([^/]+)\/messages$/)
  if (messages && method === 'POST') {
    const application = findApplication(decodeURIComponent(messages[1]))
    const body = await readJson(request)
    const fields = validateMessage(body)
    const messageTopic = text(body, 'topic')
    const activeTopic = state.topics.some(
      (candidate) =>
        candidate.application_id === application.id &&
        candidate.id === messageTopic &&
        candidate.channel === text(body, 'channel') &&
        candidate.archived_at === null,
    )
    if (messageTopic !== '' && !activeTopic && !fields.topic) {
      fields.topic = 'unknown or archived topic'
    }
    if (Object.keys(fields).length > 0) {
      throw new HttpError(422, 'invalid_fields', 'message fields are invalid', fields)
    }

    const idempotencyKey = text(body, 'idempotency_key')
    const existing = state.messages.find(
      (candidate) => candidate.application_id === application.id && candidate.idempotency_key === idempotencyKey,
    )
    if (existing) {
      return send(response, 202, { message_id: existing.id, device_count: existing.device_count, deduplicated: true })
    }

    const channel = text(body, 'channel')
    const deviceCount = state.devices.filter(
      (device) =>
        device.application_id === application.id &&
        device.channel === channel &&
        (device.topic === '' || device.topic === messageTopic),
    ).length
    const published: Message = {
      application_id: application.id,
      idempotency_key: idempotencyKey,
      id: randomUUID(),
      status: 'published',
      channel,
      topic: messageTopic,
      priority: body.priority === 'high' ? 'high' : 'normal',
      title: text(body, 'title'),
      body: text(body, 'body'),
      link: text(body, 'link'),
      device_count: deviceCount,
      publisher: { type: 'admin', name: session.username },
      delivery_counts: counts({ pending: deviceCount }),
      delivery_errors: [],
      created_at: new Date().toISOString(),
      expires_at: text(body, 'expires_at'),
    }
    state.messages.push(published)
    return send(response, 202, { message_id: published.id, device_count: deviceCount, deduplicated: false })
  }

  if (messages && method === 'GET') {
    const application = findApplication(decodeURIComponent(messages[1]))
    const query = new URL(request.url ?? '/', 'http://localhost').searchParams
    const limit = Number(query.get('limit') ?? 20)
    if (!Number.isInteger(limit) || limit < 1 || limit > 100) {
      throw new HttpError(400, 'invalid_query', 'limit must be between 1 and 100')
    }
    const cursor = query.get('cursor')
    const offset = cursor === null ? 0 : Number(Buffer.from(cursor, 'base64url').toString())
    if (!Number.isInteger(offset) || offset < 0) {
      throw new HttpError(400, 'invalid_query', 'invalid history cursor')
    }
    const history = state.messages
      .filter((candidate) => candidate.application_id === application.id)
      .sort((a, b) => b.created_at.localeCompare(a.created_at))
    const page = history.slice(offset, offset + limit)
    const next = offset + limit < history.length ? Buffer.from(String(offset + limit)).toString('base64url') : null
    return send(response, 200, { messages: page.map(publicMessage), next_cursor: next })
  }

  const routing = path.match(/^\/api\/applications\/([^/]+)\/routing$/)
  if (routing && method === 'GET') {
    const application = findApplication(decodeURIComponent(routing[1]))
    const topics = state.topics.filter((candidate) => candidate.application_id === application.id)
    return send(response, 200, { channels, topics: topics.map(publicTopic) })
  }

  const topics = path.match(/^\/api\/applications\/([^/]+)\/topics$/)
  if (topics && method === 'POST') {
    const application = findApplication(decodeURIComponent(topics[1]))
    const body = await readJson(request)
    const id = text(body, 'id')
    const channel = text(body, 'channel')
    const fields: Record<string, string> = {}
    if (id.length > 200 || !/^[a-z0-9]+(?:[._-][a-z0-9]+)*$/.test(id)) {
      fields.id = 'must use lowercase letters, numbers, dots, underscores, and hyphens'
    }
    if (!channels.some((candidate) => candidate.id === channel)) {
      fields.channel = 'unknown channel'
    }
    if (Object.keys(fields).length > 0) {
      throw new HttpError(422, 'invalid_fields', 'topic fields are invalid', fields)
    }
    const label = validateLabel(body)
    if (state.topics.some((candidate) => candidate.application_id === application.id && candidate.id === id)) {
      throw new HttpError(409, 'topic_exists', 'topic ID is already in use')
    }
    const created = topic(application, id, label, channel)
    state.topics.push(created)
    return send(response, 201, { topic: publicTopic(created) })
  }

  const topicPath = path.match(/^\/api\/applications\/([^/]+)\/topics\/([^/]+)(\/archive)?$/)
  const archive = topicPath?.[3] !== undefined
  if (topicPath && ((method === 'PATCH' && !archive) || (method === 'POST' && archive))) {
    const application = findApplication(decodeURIComponent(topicPath[1]))
    const found = findTopic(application, decodeURIComponent(topicPath[2]))
    if (found.archived_at) throw new HttpError(409, 'already_archived', 'topic is already archived')
    if (archive) found.archived_at = new Date().toISOString()
    else found.label = validateLabel(await readJson(request))
    return send(response, 200, { topic: publicTopic(found) })
  }

  throw new HttpError(404, 'not_found', 'not found')
}

function validateMessage(body: Record<string, unknown>) {
  const fields: Record<string, string> = {}
  const limits: [string, number, boolean][] = [
    ['idempotency_key', 200, true],
    ['channel', 64, true],
    ['topic', 200, false],
    ['title', 200, true],
    ['body', 2000, true],
    ['link', 500, false],
    ['collapse_key', 200, false],
  ]
  for (const [name, max, required] of limits) {
    const value = text(body, name)
    if (required && value === '') fields[name] = 'is required'
    else if (value.length > max) fields[name] = 'exceeds the maximum length'
  }

  const link = text(body, 'link')
  if (link && (!link.startsWith('/') || link.startsWith('//') || link.startsWith('/\\'))) {
    fields.link = 'must be an application-relative path'
  }
  const priority = body.priority ?? ''
  if (priority !== '' && priority !== 'normal' && priority !== 'high') {
    fields.priority = 'must be normal or high'
  }
  const data = body.data ?? {}
  if (typeof data !== 'object' || data === null || Array.isArray(data)) {
    fields.data = 'data must be valid JSON'
  }
  const expiresAt = Date.parse(text(body, 'expires_at'))
  if (Number.isNaN(expiresAt)) fields.expires_at = 'is required'
  else if (expiresAt <= Date.now()) fields.expires_at = 'expires_at must be in the future'
  if (Object.keys(fields).length === 0 && JSON.stringify(body).length > 3500) {
    fields.request = 'message exceeds the 3500-byte push delivery limit'
  }
  return fields
}

function handleControl(request: IncomingMessage, response: ServerResponse, url: URL) {
  if (url.pathname === '/__mock/health') return send(response, 200, { status: 'ok' })
  if (request.method !== 'POST') return send(response, 405)

  if (url.pathname === '/__mock/reset') {
    state = seed(url.searchParams.get('seed') ?? 'default')
    return send(response, 204)
  }
  if (url.pathname === '/__mock/expire-sessions') {
    state.sessions.clear()
    return send(response, 204)
  }
  return send(response, 404)
}

createServer(async (request, response) => {
  const url = new URL(request.url ?? '/', `http://localhost:${port}`)
  response.setHeader('Cache-Control', 'no-store')

  if (url.pathname.startsWith('/__mock/')) return handleControl(request, response, url)
  if (!url.pathname.startsWith('/api/')) return send(response, 404, { error: 'not found' })

  if (request.headers.origin === adminOrigin) {
    response.setHeader('Access-Control-Allow-Origin', adminOrigin)
    response.setHeader('Access-Control-Allow-Credentials', 'true')
    response.setHeader('Access-Control-Expose-Headers', 'Retry-After')
    response.setHeader('Vary', 'Origin')
  }
  if (request.method === 'OPTIONS') {
    response.setHeader('Access-Control-Allow-Methods', 'GET, POST, PATCH, DELETE')
    response.setHeader('Access-Control-Allow-Headers', 'Content-Type')
    return send(response, 204)
  }

  try {
    await handleApi(request, response, url.pathname)
  } catch (error) {
    if (error instanceof HttpError) {
      return send(response, error.status, {
        error: error.message,
        code: error.code,
        ...(error.fields && { fields: error.fields }),
      })
    }
    send(response, 500, { error: 'internal server error', code: 'internal_error' })
  }
}).listen(port, () => {
  console.log(`Herald admin mock API on http://localhost:${port}/api (UI origin ${adminOrigin})`)
})
