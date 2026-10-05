import type { Page } from '@playwright/test'
import { API_URL, expect, expectTokenNowhere, signIn, snap, test } from './fixtures'

const cors = {
  'Access-Control-Allow-Origin': 'http://localhost:4173',
  'Access-Control-Allow-Credentials': 'true',
}

async function openSample(page: Page) {
  await signIn(page)
  await page.getByRole('link', { name: 'Applications' }).click()
  await page.getByRole('link', { name: 'sample', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Sample' })).toBeVisible()
}

async function createProducer(page: Page, name: string) {
  await page.getByLabel('Producer name').fill(name)
  await page.getByRole('button', { name: 'Create producer' }).click()
  const token = page.getByTestId('producer-token')
  await expect(token).toBeVisible()
  return (await token.textContent()) ?? ''
}

test('producers are listed with their state', async ({ page }, testInfo) => {
  await openSample(page)

  const active = page.getByRole('row', { name: /sample-api/ })
  await expect(active.getByText('Active')).toBeVisible()
  await expect(active.getByRole('button', { name: 'Revoke sample-api' })).toBeVisible()

  const revoked = page.getByRole('row', { name: /sample-legacy/ })
  await expect(revoked.getByText('Revoked', { exact: true })).toBeVisible()
  await expect(revoked.getByRole('button')).toHaveCount(0)
  await snap(page, testInfo, 'producers')
})

test('an application with no producers says so', async ({ page }) => {
  await signIn(page)
  await page.goto('/applications/other')
  await expect(page.getByText('No producers yet.')).toBeVisible()
})

test('an unknown application shows not found', async ({ page }, testInfo) => {
  await signIn(page)
  await page.goto('/applications/missing')
  await expect(page.getByText('Application not found')).toBeVisible()
  await snap(page, testInfo, 'application-not-found')
})

test('a new producer token is shown once with a warning and can be copied', async ({
  page,
}, testInfo) => {
  await openSample(page)
  const token = await createProducer(page, 'sample-worker')

  expect(token).toMatch(/^[A-Za-z0-9_-]{43}$/)
  await expect(page.getByText(/cannot be recovered/)).toBeVisible()
  await expect(page.getByRole('row', { name: /sample-worker/ }).getByText('Active')).toBeVisible()
  await expect(page.getByLabel('Producer name')).toHaveValue('')
  await snap(page, testInfo, 'token-shown')

  await page.getByRole('button', { name: 'Copy token' }).click()
  await expect(page.getByRole('button', { name: 'Copied' })).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(token)
})

test('dismissing the token removes it from the page', async ({
  page,
  consoleLines,
  requestedUrls,
}, testInfo) => {
  await openSample(page)
  const token = await createProducer(page, 'sample-worker')

  await page.getByRole('button', { name: 'I have stored the token' }).click()

  await expect(page.getByTestId('producer-token')).toHaveCount(0)
  await expectTokenNowhere(page, token, consoleLines, requestedUrls)
  await snap(page, testInfo, 'token-dismissed')
})

test('leaving the page clears the token', async ({ page, consoleLines, requestedUrls }) => {
  await openSample(page)
  const token = await createProducer(page, 'sample-worker')

  await page.getByRole('link', { name: 'Applications' }).click()
  await page.getByRole('link', { name: 'sample', exact: true }).click()

  await expect(page.getByRole('row', { name: /sample-worker/ })).toBeVisible()
  await expect(page.getByTestId('producer-token')).toHaveCount(0)
  await expectTokenNowhere(page, token, consoleLines, requestedUrls)
})

test('the token does not survive a reload and is never persisted', async ({
  page,
  consoleLines,
  requestedUrls,
}, testInfo) => {
  await openSample(page)
  const token = await createProducer(page, 'sample-worker')

  await page.reload()

  await expect(page.getByRole('row', { name: /sample-worker/ })).toBeVisible()
  await expect(page.getByTestId('producer-token')).toHaveCount(0)
  await expectTokenNowhere(page, token, consoleLines, requestedUrls)
  await snap(page, testInfo, 'token-after-reload')
})

test('a duplicate active producer name is rejected without a token', async ({ page }) => {
  await openSample(page)
  await page.getByLabel('Producer name').fill('sample-api')
  await page.getByRole('button', { name: 'Create producer' }).click()

  await expect(page.getByText('An active producer with this name already exists.')).toBeVisible()
  await expect(page.getByTestId('producer-token')).toHaveCount(0)
  await expect(page.getByLabel('Producer name')).toHaveValue('sample-api')
})

test('cancelling the revoke confirmation changes nothing', async ({
  page,
  requestedUrls,
}, testInfo) => {
  await openSample(page)
  await page.getByRole('button', { name: 'Revoke sample-api' }).click()

  const dialog = page.getByRole('alertdialog', { name: 'Revoke sample-api?' })
  await expect(dialog).toBeVisible()
  await snap(page, testInfo, 'revoke-confirm')
  await dialog.getByRole('button', { name: 'Cancel' }).click()

  await expect(dialog).toHaveCount(0)
  await expect(page.getByRole('row', { name: /sample-api/ }).getByText('Active')).toBeVisible()
  expect(requestedUrls.filter((url) => url.endsWith('/revoke'))).toEqual([])
})

test('confirming the revoke marks the producer revoked', async ({ page }, testInfo) => {
  await openSample(page)
  await page.getByRole('button', { name: 'Revoke sample-api' }).click()
  await page
    .getByRole('alertdialog', { name: 'Revoke sample-api?' })
    .getByRole('button', { name: 'Revoke producer' })
    .click()

  const row = page.getByRole('row', { name: /sample-api/ })
  await expect(row.getByText('Revoked', { exact: true })).toBeVisible()
  await expect(row.getByRole('button')).toHaveCount(0)
  await snap(page, testInfo, 'revoked')

  await page.reload()
  await expect(
    page.getByRole('row', { name: /sample-api/ }).getByText('Revoked', { exact: true }),
  ).toBeVisible()
})

test('a failed revoke is reported and the producer stays active', async ({ page }, testInfo) => {
  await openSample(page)
  await page.route(`${API_URL}/applications/sample/producers/*/revoke`, (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      headers: cors,
      body: JSON.stringify({ error: 'internal error', code: 'internal' }),
    }),
  )

  await page.getByRole('button', { name: 'Revoke sample-api' }).click()
  await page
    .getByRole('alertdialog', { name: 'Revoke sample-api?' })
    .getByRole('button', { name: 'Revoke producer' })
    .click()

  await expect(page.getByRole('alert')).toHaveText(/internal error/)
  await expect(page.getByRole('row', { name: /sample-api/ }).getByText('Active')).toBeVisible()
  await snap(page, testInfo, 'revoke-failed')
})

test('a conflicting revoke reports the server error', async ({ page }) => {
  await openSample(page)
  await page.route(`${API_URL}/applications/sample/producers/*/revoke`, (route) =>
    route.fulfill({
      status: 409,
      contentType: 'application/json',
      headers: cors,
      body: JSON.stringify({ error: 'producer is already revoked', code: 'already_revoked' }),
    }),
  )

  await page.getByRole('button', { name: 'Revoke sample-api' }).click()
  await page
    .getByRole('alertdialog', { name: 'Revoke sample-api?' })
    .getByRole('button', { name: 'Revoke producer' })
    .click()

  await expect(page.getByRole('alert')).toHaveText(/producer is already revoked/)
})
