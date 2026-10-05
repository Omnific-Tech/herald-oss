import { API_URL, expect, signIn, snap, test } from './fixtures'

test('analytics is a placeholder and requests nothing', async ({
  page,
  requestedUrls,
}, testInfo) => {
  await signIn(page)
  const before = requestedUrls.length

  await page.getByRole('link', { name: 'Analytics' }).click()

  await expect(page.getByRole('heading', { name: 'Analytics' })).toBeVisible()
  await expect(page.getByText('No analytics endpoint yet')).toBeVisible()
  expect(requestedUrls.slice(before).filter((url) => url.startsWith(API_URL))).toEqual([])
  await snap(page, testInfo, 'analytics')
})
