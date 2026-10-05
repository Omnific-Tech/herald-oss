import { API_URL, expect, signIn, snap, test } from './fixtures'

test('no applications shows an empty state with a way to create one', async ({
  page,
  mock,
}, testInfo) => {
  await mock.reset('empty')
  await signIn(page)
  await page.getByRole('link', { name: 'Applications' }).click()

  await expect(page.getByText('No applications yet.')).toBeVisible()
  await expect(page.getByRole('link', { name: 'New application' })).toBeVisible()
  await snap(page, testInfo, 'applications-empty')
})

test('applications are listed with slug, name, and origin', async ({ page }, testInfo) => {
  await signIn(page)
  await page.getByRole('link', { name: 'Applications' }).click()

  const sample = page.getByRole('row', { name: /sample/ })
  await expect(sample.getByRole('cell', { name: 'Sample', exact: true })).toBeVisible()
  await expect(sample.getByRole('cell', { name: 'https://sample.example' })).toBeVisible()
  await expect(page.getByRole('row', { name: /other/ })).toBeVisible()
  await snap(page, testInfo, 'applications')
})

test('creating an application opens it and adds it to the list', async ({ page }, testInfo) => {
  await signIn(page)
  await page.getByRole('link', { name: 'Applications' }).click()
  await page.getByRole('link', { name: 'New application' }).click()

  await page.getByLabel('Slug').fill('orbit')
  await page.getByLabel('Display name').fill('Orbit')
  await page.getByLabel('Origin').fill('https://orbit.example')
  await page.getByLabel('VAPID subject').fill('mailto:ops@orbit.example')
  await snap(page, testInfo, 'application-form')
  await page.getByRole('button', { name: 'Create application' }).click()

  await expect(page.getByRole('heading', { name: 'Orbit' })).toBeVisible()
  await expect(page).toHaveURL(/\/applications\/orbit$/)

  await page.getByRole('link', { name: 'Applications' }).click()
  await expect(page.getByRole('row', { name: /orbit/ })).toBeVisible()
})

test('a duplicate slug is reported on the field and the form keeps its input', async ({
  page,
}, testInfo) => {
  await signIn(page)
  await page.goto('/applications/new')

  await page.getByLabel('Slug').fill('sample')
  await page.getByLabel('Display name').fill('Sample again')
  await page.getByLabel('Origin').fill('https://sample2.example')
  await page.getByLabel('VAPID subject').fill('mailto:ops@sample2.example')
  await page.getByRole('button', { name: 'Create application' }).click()

  await expect(page.getByText('An application with this slug already exists.')).toBeVisible()
  await expect(page.getByLabel('Display name')).toHaveValue('Sample again')
  await expect(page.getByLabel('Slug')).toHaveAttribute('aria-invalid', 'true')
  await snap(page, testInfo, 'application-duplicate')
})

test('server validation errors appear next to each field', async ({ page }, testInfo) => {
  await signIn(page)
  await page.goto('/applications/new')

  await page.getByLabel('Slug').fill('Bad Slug')
  await page.getByLabel('Display name').fill('Bad')
  await page.getByLabel('Origin').fill('http://insecure.example/path')
  await page.getByLabel('VAPID subject').fill('ops@example')
  await page.getByRole('button', { name: 'Create application' }).click()

  await expect(page.getByText('must use lowercase letters, numbers, and single hyphens')).toBeVisible()
  await expect(page.getByText('must be an HTTPS origin without a path, query, or fragment')).toBeVisible()
  await expect(page.getByText('must be a mailto: or HTTPS URL')).toBeVisible()
  await expect(page.getByLabel('Slug')).toHaveValue('Bad Slug')
  await snap(page, testInfo, 'application-invalid')
})

test('a failed list request shows an error that can be retried', async ({ page }, testInfo) => {
  await signIn(page)
  await page.route(`${API_URL}/applications`, (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      headers: {
        'Access-Control-Allow-Origin': 'http://localhost:4173',
        'Access-Control-Allow-Credentials': 'true',
      },
      body: JSON.stringify({ error: 'internal error', code: 'internal' }),
    }),
  )
  await page.getByRole('link', { name: 'Applications' }).click()

  await expect(page.getByRole('alert')).toHaveText(/internal error/)
  await snap(page, testInfo, 'applications-failed')

  await page.unroute(`${API_URL}/applications`)
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('row', { name: /sample/ })).toBeVisible()
})
