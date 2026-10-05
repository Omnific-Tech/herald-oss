import { test as base, expect, type Page, type TestInfo } from '@playwright/test'

export const MOCK_URL = 'http://localhost:4012'
export const API_URL = `${MOCK_URL}/api`
export const USERNAME = 'admin'
export const PASSWORD = 'herald-dev'

type Mock = {
  reset: (seed?: 'default' | 'empty') => Promise<void>
  expireSessions: () => Promise<void>
}

type Fixtures = {
  mock: Mock
  consoleLines: string[]
  requestedUrls: string[]
}

export const test = base.extend<Fixtures>({
  mock: [
    async ({ request }, use) => {
      const mock: Mock = {
        reset: async (seed = 'default') => {
          const response = await request.post(`${MOCK_URL}/__mock/reset?seed=${seed}`)
          expect(response.ok()).toBe(true)
        },
        expireSessions: async () => {
          const response = await request.post(`${MOCK_URL}/__mock/expire-sessions`)
          expect(response.ok()).toBe(true)
        },
      }
      await mock.reset()
      await use(mock)
    },
    { auto: true },
  ],
  consoleLines: async ({ page }, use) => {
    const lines: string[] = []
    page.on('console', (message) => lines.push(message.text()))
    page.on('pageerror', (error) => lines.push(error.message))
    await use(lines)
  },
  requestedUrls: async ({ page }, use) => {
    const urls: string[] = []
    page.on('request', (request) => urls.push(request.url()))
    await use(urls)
  },
})

export { expect }

export async function signIn(page: Page) {
  await page.goto('/login')
  await page.getByLabel('Username').fill(USERNAME)
  await page.getByLabel('Password').fill(PASSWORD)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
}

export async function snap(page: Page, testInfo: TestInfo, name: string) {
  await testInfo.attach(name, {
    body: await page.screenshot({ fullPage: true }),
    contentType: 'image/png',
  })
}

export async function expectTokenNowhere(
  page: Page,
  token: string,
  consoleLines: string[],
  requestedUrls: string[],
) {
  const hits = await page.evaluate(async (secret) => {
    const found: string[] = []
    for (const [name, storage] of [
      ['localStorage', localStorage],
      ['sessionStorage', sessionStorage],
    ] as const) {
      for (let index = 0; index < storage.length; index++) {
        const key = storage.key(index) ?? ''
        if (key.includes(secret) || (storage.getItem(key) ?? '').includes(secret)) {
          found.push(name)
        }
      }
    }
    const databases = await indexedDB.databases()
    if (databases.length > 0) found.push(`indexedDB: ${databases.map((db) => db.name).join(', ')}`)
    const cacheNames = await caches.keys()
    if (cacheNames.length > 0) found.push(`CacheStorage: ${cacheNames.join(', ')}`)
    if (location.href.includes(secret)) found.push('url')
    if (document.title.includes(secret)) found.push('title')
    if (document.documentElement.outerHTML.includes(secret)) found.push('dom')
    return found
  }, token)
  expect(hits).toEqual([])

  const cookies = await page.context().cookies()
  expect(cookies.filter((cookie) => cookie.value.includes(token))).toEqual([])
  expect(consoleLines.filter((line) => line.includes(token))).toEqual([])
  expect(requestedUrls.filter((url) => url.includes(token))).toEqual([])
}
