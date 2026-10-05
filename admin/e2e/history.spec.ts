import type { Page } from '@playwright/test'
import { API_URL, expect, signIn, snap, test } from './fixtures'

const cors = {
  'Access-Control-Allow-Origin': 'http://localhost:4173',
  'Access-Control-Allow-Credentials': 'true',
}

const serverError = {
  status: 500,
  contentType: 'application/json',
  headers: cors,
  body: JSON.stringify({ error: 'internal server error', code: 'internal_error' }),
}

async function openHistory(page: Page, app = 'sample') {
  await page.goto(`/notifications?app=${app}`)
  await expect(page.getByRole('heading', { name: 'Notifications' })).toBeVisible()
}

test.beforeEach(async ({ page }) => signIn(page))

test('a notification shows its content, routing, publisher, timing, and delivery', async ({
  page,
}, testInfo) => {
  await openHistory(page)

  const item = page.getByRole('article', { name: 'Earthquake alert' })
  await expect(item).toContainText('Magnitude 5.1 near Jakarta')
  await expect(item).toContainText('alerts / earthquake.jakarta')
  await expect(item).toContainText('high')
  await expect(item).toContainText('published')
  await expect(item).toContainText('Published by operator (admin)')
  await expect(item).toContainText('20 Sept 2026, 08:00 UTC')
  await expect(item).toContainText('Expired 20 Sept 2026, 08:10 UTC')
  await expect(item).toContainText('/events/123')
  await expect(item).toContainText('3 devices targeted')
  await expect(item).toContainText('Sent 1')
  await expect(item).toContainText('Displayed 1')
  await expect(item).not.toContainText('Failed')
  await expect(item).not.toContainText('Delivery errors')

  const producer = page.getByRole('article', { name: 'Bitcoin crossed 100k' })
  await expect(producer).toContainText('alerts / btc')
  await expect(producer).toContainText('Published by sample-api (producer)')
  await snap(page, testInfo, 'history')
})

test('delivery errors show whether each reason is retrying or final', async ({ page }, testInfo) => {
  await openHistory(page)

  const item = page.getByRole('article', { name: 'Scheduled maintenance' })
  await expect(item).toContainText('Pending 1')
  await expect(item).toContainText('Failed 3')
  await expect(item.getByRole('list', { name: 'Delivery errors' }).getByRole('listitem')).toHaveText([
    'FailedFCM 404: Requested entity was not found.2 devices',
    'FailedWeb Push HTTP 410: subscription gone1 device',
    'RetryingFCM transport error1 device',
  ])
  await snap(page, testInfo, 'history-errors')
})

test('the page is read-only', async ({ page }) => {
  await openHistory(page)
  await expect(page.getByRole('article').first()).toBeVisible()

  await expect(page.getByRole('main').getByRole('button')).toHaveText(['Load more'])
})

test('an application with no notifications says so', async ({ page }, testInfo) => {
  await openHistory(page, 'other')

  await expect(page.getByText('No notifications sent yet.')).toBeVisible()
  await expect(page.getByRole('article')).toHaveCount(0)
  await snap(page, testInfo, 'history-empty')
})

test('load more pages through the history until the end', async ({ page }) => {
  await openHistory(page)
  await expect(page.getByRole('article')).toHaveCount(20)

  await page.getByRole('button', { name: 'Load more' }).click()

  await expect(page.getByRole('article')).toHaveCount(25)
  await expect(page.getByRole('article', { name: 'Status update 25' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Load more' })).toHaveCount(0)
})

test('history is scoped to the selected application', async ({ page }) => {
  await page.goto('/applications/sample')
  await page.getByRole('link', { name: 'Notification history' }).click()
  await expect(page.getByLabel('Application')).toHaveValue('sample')
  await expect(page.getByRole('article', { name: 'Earthquake alert' })).toBeVisible()

  await page.getByLabel('Application').selectOption('other')

  await expect(page).toHaveURL(/app=other/)
  await expect(page.getByText('No notifications sent yet.')).toBeVisible()
  await expect(page.getByRole('article', { name: 'Earthquake alert' })).toHaveCount(0)
})

test('a sent notification appears at the top of the history', async ({ page }) => {
  await page.goto('/applications/other/notify')
  await page.getByLabel('Title').fill('Other maintenance')
  await page.getByLabel('Body').fill('Back in ten minutes')
  await page.getByRole('button', { name: 'Review and send' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Send notification' }).click()
  await expect(page.getByRole('status')).toBeVisible()

  await openHistory(page, 'other')

  const item = page.getByRole('article').first()
  await expect(item).toHaveAccessibleName('Other maintenance')
  await expect(item).toContainText('Published by admin (admin)')
  await expect(item).toContainText('alerts / whole channel')
  await expect(item).toContainText('0 devices targeted')
  await expect(item).toContainText('Expires ')
})

test('a failed history request can be retried', async ({ page }, testInfo) => {
  await page.route(`${API_URL}/applications/sample/messages?*`, (route) => route.fulfill(serverError))
  await openHistory(page)

  await expect(page.getByRole('alert')).toHaveText(/internal server error/)
  await snap(page, testInfo, 'history-failed')

  await page.unroute(`${API_URL}/applications/sample/messages?*`)
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('article', { name: 'Earthquake alert' })).toBeVisible()
})

test('a failed load more keeps loaded items and can be retried', async ({ page }) => {
  await openHistory(page)
  await expect(page.getByRole('article')).toHaveCount(20)
  await page.route(`${API_URL}/applications/sample/messages?*cursor=*`, (route) => route.fulfill(serverError))

  await page.getByRole('button', { name: 'Load more' }).click()

  await expect(page.getByRole('alert')).toHaveText(/internal server error/)
  await expect(page.getByRole('article')).toHaveCount(20)

  await page.unroute(`${API_URL}/applications/sample/messages?*cursor=*`)
  await page.getByRole('button', { name: 'Load more' }).click()
  await expect(page.getByRole('article')).toHaveCount(25)
})

test('an expired session while paging returns to sign-in', async ({ page, mock }) => {
  await openHistory(page)
  await expect(page.getByRole('article')).toHaveCount(20)

  await mock.expireSessions()
  await page.getByRole('button', { name: 'Load more' }).click()

  await expect(page.getByRole('heading', { name: 'Sign in to Herald' })).toBeVisible()
  await expect(page.getByRole('status')).toHaveText(/Your session expired/)
})
