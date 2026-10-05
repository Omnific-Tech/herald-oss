import type { Page, Request } from '@playwright/test'
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

async function openComposer(page: Page, slug = 'sample') {
  await page.goto(`/applications/${slug}/notify`)
  await expect(page.getByRole('heading', { name: 'Send notification' })).toBeVisible()
}

async function optionLabels(page: Page, label: string) {
  return page.getByLabel(label).locator('option').allTextContents()
}

function topicRow(page: Page, id: string) {
  return page.getByRole('row', { name: new RegExp(id.replace('.', '\\.')) })
}

test.describe('send form', () => {
  test.beforeEach(async ({ page }) => signIn(page))

  test('channels come from the application routing', async ({ page }) => {
    await openComposer(page)

    expect(await optionLabels(page, 'Channel')).toEqual(['Alerts', 'System'])
    await expect(page.getByLabel('Channel')).toHaveValue('alerts')
  })

  test('topics are the active ones on the selected channel', async ({ page }, testInfo) => {
    await openComposer(page)

    expect(await optionLabels(page, 'Topic')).toEqual([
      'Whole channel',
      'Bitcoin price (btc)',
      'Jakarta earthquakes (earthquake.jakarta)',
    ])
    await snap(page, testInfo, 'topic-select')

    await page.getByLabel('Channel').selectOption('system')
    expect(await optionLabels(page, 'Topic')).toEqual(['Whole channel', 'Maintenance windows (maintenance)'])
  })

  test('changing channel resets the topic to the whole channel', async ({ page }) => {
    await openComposer(page)
    await page.getByLabel('Topic').selectOption('btc')

    await page.getByLabel('Channel').selectOption('system')

    await expect(page.getByLabel('Topic')).toHaveValue('')
  })

  test('whole channel sends an empty topic and a topic sends its id', async ({ page }) => {
    const topics: unknown[] = []
    page.on('request', (request: Request) => {
      if (request.method() === 'POST' && request.url().endsWith('/messages')) {
        topics.push(request.postDataJSON().topic)
      }
    })
    await openComposer(page)
    await page.getByLabel('Title').fill('Test notification')
    await page.getByLabel('Body').fill('Herald delivery test')

    for (const topic of ['', 'btc']) {
      await page.getByLabel('Topic').selectOption(topic)
      await page.getByRole('button', { name: 'Review and send' }).click()
      await page.getByRole('alertdialog').getByRole('button', { name: 'Send notification' }).click()
      await expect(page.getByRole('status')).toBeVisible()
    }

    expect(topics).toEqual(['', 'btc'])
  })

  test('topic lists are scoped to one application', async ({ page }) => {
    await openComposer(page, 'other')

    expect(await optionLabels(page, 'Topic')).toEqual(['Whole channel', 'Market open (markets.open)'])

    await page.goto('/applications/other')
    await expect(topicRow(page, 'markets.open')).toBeVisible()
    await expect(topicRow(page, 'earthquake.jakarta')).toHaveCount(0)
  })

  test('a failed routing request blocks sending until retried', async ({ page }, testInfo) => {
    await page.route(`${API_URL}/applications/sample/routing`, (route) => route.fulfill(serverError))
    await openComposer(page)

    await expect(page.getByRole('alert')).toHaveText(/internal server error/)
    await expect(page.getByRole('button', { name: 'Review and send' })).toHaveCount(0)
    await snap(page, testInfo, 'routing-failed')

    await page.unroute(`${API_URL}/applications/sample/routing`)
    await page.getByRole('button', { name: 'Retry' }).click()
    await expect(page.getByRole('button', { name: 'Review and send' })).toBeVisible()
  })
})

test.describe('topic management', () => {
  test.beforeEach(async ({ page }) => {
    await signIn(page)
    await page.goto('/applications/sample')
    await expect(page.getByRole('heading', { name: 'Topics' })).toBeVisible()
  })

  test('topics are listed with their channel and state', async ({ page }, testInfo) => {
    const active = topicRow(page, 'earthquake.jakarta')
    await expect(active.getByText('Jakarta earthquakes')).toBeVisible()
    await expect(active.getByText('Alerts')).toBeVisible()
    await expect(active.getByText('Active')).toBeVisible()

    const archived = topicRow(page, 'earthquake.bandung')
    await expect(archived.getByText('Archived', { exact: true })).toBeVisible()
    await expect(archived.getByRole('button')).toHaveCount(0)
    await snap(page, testInfo, 'topics')
  })

  test('a new topic is listed and offered when sending', async ({ page }) => {
    await page.getByLabel('Topic ID').fill('earthquake.bali')
    await page.getByLabel('Label', { exact: true }).fill('Bali earthquakes')
    await page.getByLabel('Channel').selectOption('alerts')
    await page.getByRole('button', { name: 'Add topic' }).click()

    await expect(topicRow(page, 'earthquake.bali').getByText('Bali earthquakes')).toBeVisible()
    await expect(page.getByLabel('Topic ID')).toHaveValue('')

    await openComposer(page)
    expect(await optionLabels(page, 'Topic')).toContain('Bali earthquakes (earthquake.bali)')
  })

  test('topic creation errors appear on the fields', async ({ page }, testInfo) => {
    await page.getByLabel('Topic ID').fill('earthquake.bandung')
    await page.getByLabel('Label', { exact: true }).fill('Bandung again')
    await page.getByRole('button', { name: 'Add topic' }).click()
    await expect(page.getByText('A topic with this ID already exists.')).toBeVisible()

    await page.getByLabel('Topic ID').fill('Bad ID')
    await page.getByRole('button', { name: 'Add topic' }).click()
    await expect(page.getByText('must use lowercase letters, numbers, dots, underscores, and hyphens')).toBeVisible()
    await expect(page.getByLabel('Label', { exact: true })).toHaveValue('Bandung again')
    await snap(page, testInfo, 'topic-invalid')
  })

  test('editing a label updates the list and the send form', async ({ page }, testInfo) => {
    await page.getByRole('button', { name: 'Edit earthquake.jakarta' }).click()
    const input = page.getByLabel('Label for earthquake.jakarta')
    await input.fill('Jakarta area earthquakes')
    await snap(page, testInfo, 'topic-edit')
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(topicRow(page, 'earthquake.jakarta').getByText('Jakarta area earthquakes')).toBeVisible()
    await expect(input).toHaveCount(0)

    await openComposer(page)
    expect(await optionLabels(page, 'Topic')).toContain('Jakarta area earthquakes (earthquake.jakarta)')
  })

  test('cancelling an edit keeps the label', async ({ page }) => {
    await page.getByRole('button', { name: 'Edit earthquake.jakarta' }).click()
    await page.getByLabel('Label for earthquake.jakarta').fill('Something else')
    await page.getByRole('button', { name: 'Cancel' }).click()

    await expect(topicRow(page, 'earthquake.jakarta').getByText('Jakarta earthquakes')).toBeVisible()
  })

  test('a failed edit keeps the editor open with the error', async ({ page }) => {
    await page.route(`${API_URL}/applications/sample/topics/earthquake.jakarta`, (route) =>
      route.fulfill(serverError),
    )
    await page.getByRole('button', { name: 'Edit earthquake.jakarta' }).click()
    await page.getByLabel('Label for earthquake.jakarta').fill('Jakarta area earthquakes')
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(page.getByRole('alert')).toHaveText(/internal server error/)
    await expect(page.getByLabel('Label for earthquake.jakarta')).toHaveValue('Jakarta area earthquakes')
  })

  test('cancelling the archive confirmation changes nothing', async ({ page, requestedUrls }) => {
    await page.getByRole('button', { name: 'Archive btc' }).click()
    const dialog = page.getByRole('alertdialog', { name: 'Archive btc?' })
    await dialog.getByRole('button', { name: 'Cancel' }).click()

    await expect(dialog).toHaveCount(0)
    await expect(topicRow(page, 'btc').getByText('Active')).toBeVisible()
    expect(requestedUrls.filter((url) => url.endsWith('/archive'))).toEqual([])
  })

  test('archiving hides the topic from the send form', async ({ page }, testInfo) => {
    await page.getByRole('button', { name: 'Archive btc' }).click()
    await snap(page, testInfo, 'archive-confirm')
    await page
      .getByRole('alertdialog', { name: 'Archive btc?' })
      .getByRole('button', { name: 'Archive topic' })
      .click()

    await expect(topicRow(page, 'btc').getByText('Archived', { exact: true })).toBeVisible()

    await openComposer(page)
    expect(await optionLabels(page, 'Topic')).not.toContain('Bitcoin price (btc)')
  })

  test('a failed archive is reported and the topic stays active', async ({ page }) => {
    await page.route(`${API_URL}/applications/sample/topics/btc/archive`, (route) => route.fulfill(serverError))
    await page.getByRole('button', { name: 'Archive btc' }).click()
    await page
      .getByRole('alertdialog', { name: 'Archive btc?' })
      .getByRole('button', { name: 'Archive topic' })
      .click()

    await expect(page.getByRole('alert')).toHaveText(/internal server error/)
    await expect(topicRow(page, 'btc').getByText('Active')).toBeVisible()
  })

  test('an expired session while adding a topic returns to sign-in', async ({ page, mock }) => {
    await mock.expireSessions()
    await page.getByLabel('Topic ID').fill('earthquake.bali')
    await page.getByLabel('Label', { exact: true }).fill('Bali earthquakes')
    await page.getByRole('button', { name: 'Add topic' }).click()

    await expect(page.getByRole('heading', { name: 'Sign in to Herald' })).toBeVisible()
    await expect(page.getByRole('status')).toHaveText(/Your session expired/)
  })
})
