import { expect, signIn, test } from './fixtures'

test('every page title reads Admin | page name', async ({ page }) => {
  await page.goto('/login')
  await expect(page).toHaveTitle('Admin | Sign in')

  await signIn(page)
  await expect(page).toHaveTitle('Admin | Overview')

  await page.getByRole('link', { name: 'Applications' }).click()
  await expect(page).toHaveTitle('Admin | Applications')

  await page.getByRole('link', { name: 'New application' }).click()
  await expect(page).toHaveTitle('Admin | New application')

  await page.goto('/applications/sample')
  await expect(page).toHaveTitle('Admin | sample')

  await page.getByRole('link', { name: 'Send notification' }).click()
  await expect(page).toHaveTitle('Admin | Send notification')

  await page.getByRole('link', { name: 'Analytics' }).click()
  await expect(page).toHaveTitle('Admin | Analytics')
})

test('the favicon is served', async ({ page, request }) => {
  await page.goto('/login')
  const href = await page.locator('link[rel="icon"]').getAttribute('href')

  const response = await request.get(href ?? '')
  expect(response.ok()).toBe(true)
  expect(response.headers()['content-type']).toContain('image/svg+xml')
})
