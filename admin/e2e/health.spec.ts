import { API_URL, expect, signIn, snap, test } from './fixtures'

const cors = {
  'Access-Control-Allow-Origin': 'http://localhost:4173',
  'Access-Control-Allow-Credentials': 'true',
}

test('the overview shows service health and the deployed version', async ({ page }, testInfo) => {
  await signIn(page)

  const health = page.getByRole('region', { name: 'Service health' })
  await expect(health.getByText('ok', { exact: true }).first()).toBeVisible()
  await expect(health.getByText('v1.4.0-mock')).toBeVisible()
  await expect(health.getByText('herald', { exact: true })).toBeVisible()
  await snap(page, testInfo, 'overview')
})

test('a degraded service is shown as degraded, not as an error', async ({ page }, testInfo) => {
  await page.route(`${API_URL}/health`, (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/json',
      headers: cors,
      body: JSON.stringify({
        status: 'degraded',
        service: 'herald',
        version: 'v1.4.0-mock',
        database: 'unreachable',
      }),
    }),
  )
  await signIn(page)

  const health = page.getByRole('region', { name: 'Service health' })
  await expect(health.getByText('degraded')).toBeVisible()
  await expect(health.getByText('unreachable')).toBeVisible()
  await snap(page, testInfo, 'overview-degraded')
})

test('a failed health request shows an error that can be retried', async ({ page }, testInfo) => {
  await page.route(`${API_URL}/health`, (route) => route.abort('connectionrefused'))
  await signIn(page)

  const health = page.getByRole('region', { name: 'Service health' })
  await expect(health.getByRole('alert')).toHaveText(/Cannot reach the Herald API/)
  await snap(page, testInfo, 'overview-unreachable')

  await page.unroute(`${API_URL}/health`)
  await health.getByRole('button', { name: 'Retry' }).click()
  await expect(health.getByText('v1.4.0-mock')).toBeVisible()
})
