/**
 * The Development tab.
 *
 * Every other gate in this repo is blind to what this file checks. `go test`
 * proved `/api/dev/status` answers, and that is exactly the trap: the endpoint
 * can be perfect while the panel never renders, renders the wrong tab, or shows
 * a blank card because a field name drifted between the handler and the client.
 * Those failures live in the DOM, so the assertions below read the DOM.
 *
 * The gateway is booted with `DEV_REPO_ROOT` pointing at this repository, which
 * is the only way to have a real git work tree behind the panel. That means the
 * assertions cannot pin a commit hash — the repo moves — so they pin the shape
 * an operator depends on: a branch name, a rendered report, and the tab being
 * reachable by URL rather than only by clicking.
 *
 * `bun test` is the runner, so Playwright's `expect(...).toBeVisible()` is not
 * available; visibility is asserted with `waitFor()`, which throws when the
 * element never appears.
 */
import { resolve } from 'node:path'
import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { startDashboard, type Dashboard } from './harness'

let app: Dashboard
let page: Dashboard['page']

beforeAll(async () => {
  // The gateway's cwd is already the repo root, but the panel reads the
  // variable explicitly, so it is set rather than relied on.
  app = await startDashboard(20421, { DEV_REPO_ROOT: resolve(import.meta.dirname, '..', '..') })
  page = app.page
})

afterAll(async () => {
  await app.stop()
})

async function openDevelopmentTab() {
  await page.goto(`${app.baseURL}/dashboard/development`)
  await page.waitForLoadState('networkidle')
  // The heading proves the view mounted, not merely that the route exists.
  await page.getByRole('heading', { name: 'Development' }).waitFor()
}

describe('development tab', () => {
  test('is reachable by its own URL', async () => {
    await openDevelopmentTab()

    expect(page.url()).toContain('/dashboard/development')
  })

  test('shows the repository the gateway was pointed at', async () => {
    await openDevelopmentTab()

    // The cards are the panel's proof that it read a real work tree. When
    // DEV_REPO_ROOT were unset the panel would show the "No checkout
    // configured" explanation instead, and this would fail rather than silently
    // pass on an empty card.
    await page.getByText('Branch', { exact: true }).waitFor()
    await page.getByText('Last commit', { exact: true }).waitFor()

    const body = await page.locator('body').innerText()
    expect(body).not.toContain('No checkout configured')
  })

  test('renders the report the API returns', async () => {
    await openDevelopmentTab()

    // The rendered report is the same string the Telegram bot prints, so seeing
    // its heading in the DOM proves the client and the handler agree on the
    // field name. A drift there produces a blank panel that every Go test still
    // passes.
    const pre = page.locator('pre')
    await pre.waitFor()
    expect(await pre.innerText()).toContain('Rama:')
  })

  test('reads the same report the endpoint serves', async () => {
    await openDevelopmentTab()

    const response = await page.evaluate(async () => {
      const res = await fetch('/api/dev/status', { headers: { Accept: 'application/json' } })
      return { status: res.status, body: await res.json() }
    })

    expect(response.status).toBe(200)
    expect(response.body.configured).toBe(true)
    // The branch is whatever the working tree is on; the assertion is that the
    // panel had one to show, not which one it is.
    expect(typeof response.body.branch).toBe('string')
    expect(response.body.branch.length).toBeGreaterThan(0)
  })

  test('refreshes when asked', async () => {
    await openDevelopmentTab()

    const button = page.getByRole('button', { name: /Refresh|Refreshing/ })
    await button.waitFor()

    const [refresh] = await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/dev/status')),
      button.click(),
    ])
    expect(refresh.status()).toBe(200)
  })
})
