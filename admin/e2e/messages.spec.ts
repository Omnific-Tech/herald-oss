import type { Page, Request } from '@playwright/test'
import { API_URL, expect, signIn, snap, test } from './fixtures'

const cors = {
  'Access-Control-Allow-Origin': 'http://localhost:4173',
  'Access-Control-Allow-Credentials': 'true',
}

type PublishBody = Record<string, unknown>

async function openComposer(page: Page, slug = 'sample') {
  await signIn(page)
  await page.goto(`/applications/${slug}`)
  await page.getByRole('link', { name: 'Send notification' }).click()
  await expect(page.getByRole('heading', { name: 'Send notification' })).toBeVisible()
}

function publishRequests(page: Page) {
  const bodies: PublishBody[] = []
  page.on('request', (request: Request) => {
    if (request.method() === 'POST' && request.url().endsWith('/messages')) {
      bodies.push(request.postDataJSON())
    }
  })
  return bodies
}

async function fillRequired(page: Page, channel = 'alerts') {
  await page.getByLabel('Channel').selectOption(channel)
  await page.getByLabel('Title').fill('Test notification')
  await page.getByLabel('Body').fill('Herald delivery test')
}

async function reviewAndSend(page: Page) {
  await page.getByRole('button', { name: 'Review and send' }).click()
  await page
    .getByRole('alertdialog', { name: 'Send this notification?' })
    .getByRole('button', { name: 'Send notification' })
    .click()
}

test('sending a notification reports how many devices it reached', async ({ page }, testInfo) => {
  const bodies = publishRequests(page)
  await openComposer(page)
  await fillRequired(page)
  await expect(page.getByLabel('Channel')).toHaveValue('alerts')
  await expect(page.getByText('Routing category devices subscribe to.')).toBeVisible()
  await expect(page.getByText('20/2000')).toBeVisible()
  await snap(page, testInfo, 'composer')

  await page.getByRole('button', { name: 'Review and send' }).click()
  const dialog = page.getByRole('alertdialog', { name: 'Send this notification?' })
  await expect(dialog).toContainText('Every device')
  await expect(dialog).toContainText('Alerts')
  await snap(page, testInfo, 'confirm-send')
  await dialog.getByRole('button', { name: 'Send notification' }).click()

  await expect(page.getByRole('status')).toContainText('Accepted for 1 device.')
  await snap(page, testInfo, 'sent')

  expect(bodies).toHaveLength(1)
  const body = bodies[0]
  expect(body.idempotency_key).toMatch(/^admin-[0-9a-f-]{36}$/)
  expect(body).toMatchObject({
    channel: 'alerts',
    priority: 'normal',
    title: 'Test notification',
    body: 'Herald delivery test',
  })
  expect(JSON.stringify(body)).not.toContain('token')
  const expiresIn = Date.parse(String(body.expires_at)) - Date.now()
  expect(expiresIn).toBeGreaterThan(9 * 60_000)
  expect(expiresIn).toBeLessThan(11 * 60_000)
})

test('optional fields are sent and a topic widens the audience', async ({ page }) => {
  const bodies = publishRequests(page)
  await openComposer(page)
  await fillRequired(page)
  await page.getByLabel('Topic').selectOption('btc')
  await page.getByLabel('Priority').selectOption('high')
  await page.getByLabel('Link').fill('/markets/btc')
  await page.getByLabel('Data (JSON)').fill('{"symbol": "BTC"}')
  await page.getByLabel('Collapse key').fill('btc-price')
  await reviewAndSend(page)

  await expect(page.getByRole('status')).toContainText('Accepted for 2 devices.')
  expect(bodies[0]).toMatchObject({
    topic: 'btc',
    priority: 'high',
    link: '/markets/btc',
    data: { symbol: 'BTC' },
    collapse_key: 'btc-price',
  })
})

test('title and body stop at their limits and show a counter', async ({ page }) => {
  await openComposer(page)
  await page.getByLabel('Title').fill('x'.repeat(250))
  await page.getByLabel('Body').fill('y'.repeat(2100))

  await expect(page.getByLabel('Title')).toHaveValue('x'.repeat(200))
  await expect(page.getByText('200/200')).toBeVisible()
  await expect(page.getByText('2000/2000')).toBeVisible()
})

test('a payload over the push size limit explains why', async ({ page }) => {
  await openComposer(page)
  await fillRequired(page)
  await page.getByLabel('Data (JSON)').fill(JSON.stringify({ blob: 'z'.repeat(4000) }))
  await reviewAndSend(page)

  await expect(page.getByRole('alert')).toHaveText(/message exceeds the 3500-byte push delivery limit/)
  await expect(page.getByRole('status')).toHaveCount(0)
})

test('cancelling the confirmation sends nothing', async ({ page }) => {
  const bodies = publishRequests(page)
  await openComposer(page)
  await fillRequired(page)

  await page.getByRole('button', { name: 'Review and send' }).click()
  const dialog = page.getByRole('alertdialog', { name: 'Send this notification?' })
  await dialog.getByRole('button', { name: 'Cancel' }).click()

  await expect(dialog).toHaveCount(0)
  await expect(page.getByRole('status')).toHaveCount(0)
  expect(bodies).toEqual([])
})

test('invalid JSON data is caught before anything is sent', async ({ page }) => {
  const bodies = publishRequests(page)
  await openComposer(page)
  await fillRequired(page)
  await page.getByLabel('Data (JSON)').fill('{not json')

  await page.getByRole('button', { name: 'Review and send' }).click()

  await expect(page.getByText('must be a JSON object')).toBeVisible()
  await expect(page.getByRole('alertdialog')).toHaveCount(0)
  expect(bodies).toEqual([])
})

test('server validation errors appear next to each field', async ({ page }, testInfo) => {
  await openComposer(page)
  await fillRequired(page)
  await page.getByLabel('Link').fill('https://evil.example/x')
  await reviewAndSend(page)

  await expect(page.getByText('must be an application-relative path')).toBeVisible()
  await expect(page.getByLabel('Link')).toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByLabel('Title')).toHaveValue('Test notification')
  await expect(page.getByRole('status')).toHaveCount(0)
  await snap(page, testInfo, 'send-invalid')
})

test('a retry after a lost response reuses the key and is deduplicated', async ({
  page,
}, testInfo) => {
  const bodies = publishRequests(page)
  let loseResponse = true
  await page.route(`${API_URL}/applications/sample/messages`, async (route) => {
    if (!loseResponse) return route.continue()
    loseResponse = false
    await route.fetch()
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      headers: cors,
      body: JSON.stringify({ error: 'internal server error', code: 'internal_error' }),
    })
  })
  await openComposer(page)
  await fillRequired(page)

  await reviewAndSend(page)
  await expect(page.getByRole('alert')).toHaveText(/internal server error/)

  await reviewAndSend(page)
  await expect(page.getByRole('status')).toContainText(
    'Already accepted earlier with this idempotency key. Nothing new was sent.',
  )
  await snap(page, testInfo, 'deduplicated')

  expect(bodies).toHaveLength(2)
  expect(bodies[1].idempotency_key).toBe(bodies[0].idempotency_key)
})

test('each successful send gets a fresh idempotency key', async ({ page }) => {
  const bodies = publishRequests(page)
  await openComposer(page)
  await fillRequired(page)

  await reviewAndSend(page)
  await expect(page.getByRole('status')).toContainText('Accepted for 1 device.')
  await reviewAndSend(page)
  await expect(page.getByRole('status')).toContainText('Accepted for 1 device.')

  expect(bodies).toHaveLength(2)
  expect(bodies[1].idempotency_key).not.toBe(bodies[0].idempotency_key)
})

test('the form cannot be submitted twice while a send is pending', async ({ page }) => {
  const bodies = publishRequests(page)
  let release = () => {}
  const released = new Promise<void>((resolve) => (release = resolve))
  await page.route(`${API_URL}/applications/sample/messages`, async (route) => {
    await released
    await route.continue()
  })
  await openComposer(page)
  await fillRequired(page)

  await reviewAndSend(page)
  await expect(page.getByRole('button', { name: 'Sending…' })).toBeDisabled()

  release()
  await expect(page.getByRole('status')).toContainText('Accepted for 1 device.')
  expect(bodies).toHaveLength(1)
})

test('a send that matches no devices says nothing will be delivered', async ({
  page,
}, testInfo) => {
  await openComposer(page, 'other')
  await fillRequired(page)
  await reviewAndSend(page)

  await expect(page.getByRole('status')).toContainText(
    'Accepted, but no subscribed devices matched. Nothing will be delivered.',
  )
  await snap(page, testInfo, 'zero-recipients')
})

test('an expired session returns to sign-in without sending', async ({ page, mock }) => {
  const bodies = publishRequests(page)
  await openComposer(page)
  await fillRequired(page)

  await mock.expireSessions()
  await reviewAndSend(page)

  await expect(page.getByRole('heading', { name: 'Sign in to Herald' })).toBeVisible()
  await expect(page.getByRole('status')).toHaveText(/Your session expired/)
  expect(bodies).toHaveLength(1)
})

test('a server failure is reported and the draft is kept', async ({ page }, testInfo) => {
  await page.route(`${API_URL}/applications/sample/messages`, (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      headers: cors,
      body: JSON.stringify({ error: 'internal server error', code: 'internal_error' }),
    }),
  )
  await openComposer(page)
  await fillRequired(page)
  await reviewAndSend(page)

  await expect(page.getByRole('alert')).toHaveText(/internal server error/)
  await expect(page.getByLabel('Body')).toHaveValue('Herald delivery test')
  await expect(page.getByRole('status')).toHaveCount(0)
  await snap(page, testInfo, 'send-failed')
})
