import { API_URL, expect, PASSWORD, signIn, snap, test, USERNAME } from './fixtures'

test('an anonymous visitor sees the sign-in view and no admin data is fetched', async ({
  page,
  requestedUrls,
}, testInfo) => {
  await page.goto('/applications')

  await expect(page.getByRole('heading', { name: 'Sign in to Herald' })).toBeVisible()
  await expect(page).toHaveURL(/\/login/)
  const apiCalls = requestedUrls.filter((url) => url.startsWith(API_URL))
  expect(apiCalls).toEqual([`${API_URL}/session`])
  await snap(page, testInfo, 'sign-in')
})

test('wrong credentials show a generic error and keep the username', async ({ page }, testInfo) => {
  await page.goto('/login')
  await page.getByLabel('Username').fill(USERNAME)
  await page.getByLabel('Password').fill('wrong-password')
  await page.getByRole('button', { name: 'Sign in' }).click()

  await expect(page.getByRole('alert')).toHaveText(/Invalid username or password/)
  await expect(page.getByLabel('Username')).toHaveValue(USERNAME)
  await expect(page.getByLabel('Password')).toHaveValue('')
  await snap(page, testInfo, 'invalid-credentials')
})

test('repeated failures surface the rate limit and its retry delay', async ({ page }, testInfo) => {
  await page.goto('/login')
  for (let attempt = 0; attempt < 6; attempt++) {
    await page.getByLabel('Username').fill(USERNAME)
    await page.getByLabel('Password').fill('wrong-password')
    await Promise.all([
      page.waitForResponse((response) => response.url() === `${API_URL}/session`),
      page.getByRole('button', { name: 'Sign in' }).click(),
    ])
  }

  await expect(page.getByRole('alert')).toHaveText(/Too many sign-in attempts\. Try again in 30 seconds/)
  await snap(page, testInfo, 'rate-limited')
})

test('signing in shows the overview and signing out ends the server session', async ({
  page,
}, testInfo) => {
  await signIn(page)
  await expect(page.getByText(USERNAME, { exact: true })).toBeVisible()
  await snap(page, testInfo, 'signed-in')

  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('heading', { name: 'Sign in to Herald' })).toBeVisible()
  await expect(page.getByRole('status')).toHaveText(/Signed out/)

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Sign in to Herald' })).toBeVisible()
})

test('a session that expires mid-use returns to sign-in and clears what was shown', async ({
  page,
  mock,
}, testInfo) => {
  await signIn(page)
  await page.getByRole('link', { name: 'Applications' }).click()
  await expect(page.getByRole('cell', { name: 'sample', exact: true })).toBeVisible()

  await mock.expireSessions()
  await page.getByRole('link', { name: 'Overview' }).click()

  await expect(page.getByRole('heading', { name: 'Sign in to Herald' })).toBeVisible()
  await expect(page.getByRole('status')).toHaveText(/Your session expired/)
  await snap(page, testInfo, 'session-expired')

  await page.getByLabel('Username').fill(USERNAME)
  await page.getByLabel('Password').fill(PASSWORD)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
})

test('a 403 from any admin request returns to sign-in as not authorized', async ({
  page,
}, testInfo) => {
  await signIn(page)
  await page.route(`${API_URL}/applications`, (route) =>
    route.fulfill({
      status: 403,
      contentType: 'application/json',
      headers: { 'Access-Control-Allow-Origin': 'http://localhost:4173', 'Access-Control-Allow-Credentials': 'true' },
      body: JSON.stringify({ error: 'origin rejected', code: 'origin_rejected' }),
    }),
  )

  await page.getByRole('link', { name: 'Applications' }).click()

  await expect(page.getByRole('heading', { name: 'Sign in to Herald' })).toBeVisible()
  await expect(page.getByRole('status')).toHaveText(/not authorized/)
  await snap(page, testInfo, 'forbidden')
})

test('an unreachable API on sign-in is reported, not treated as bad credentials', async ({
  page,
}) => {
  await page.goto('/login')
  await page.route(`${API_URL}/session`, (route) => route.abort('connectionrefused'))
  await page.getByLabel('Username').fill(USERNAME)
  await page.getByLabel('Password').fill(PASSWORD)
  await page.getByRole('button', { name: 'Sign in' }).click()

  await expect(page.getByRole('alert')).toHaveText(/Cannot reach the Herald API/)
})
