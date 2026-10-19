import { createHmac } from 'node:crypto'
import { readFileSync } from 'node:fs'

import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

import { pseudoize } from '../src/i18n/pseudo'
import { selfSigned } from './certs'

// Every page, in the pseudo-locale (src/i18n/pseudo.ts): all text a user
// sees must carry the markers, none of it may spill out of its box, and axe
// may find nothing that keeps a page from being accessible.

const english = JSON.parse(readFileSync(new URL('../../i18n/locales/en-US.json', import.meta.url), 'utf8')) as Record<string, unknown>

/** label is how a plain message reads in the pseudo-locale, to find a button by its name. */
function label(key: string): string {
  return message(key, {})
}

// pick chooses a type in a provider dialog and waits for its form: until
// then the last type's form stays, inert, and what is typed into it is lost.
async function pick(dialog: Locator, name: string): Promise<void> {
  await dialog.getByRole('option', { name }).click()
  await expect(dialog.locator('form [inert]')).toHaveCount(0)
}

/** message is how a message with simple parameters, not plurals, reads in the pseudo-locale. */
function message(key: string, params: Record<string, string>): string {
  const text = key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown>)[part], english)
  return pseudoize(Object.entries(params).reduce((s, [name, value]) => s.replace(`{${name}}`, value), String(text)))
}

// violations are what the browser refused under the server's
// Content-Security-Policy, which the UI must keep to.
let violations: string[] = []

test.beforeEach(async ({ page }) => {
  violations = []
  // The browser's own reports of violations do not reach the console
  // events, so the page tells of them.
  page.on('console', (msg) => {
    if (msg.type() === 'error' && msg.text().startsWith('CSP violation:')) {
      violations.push(msg.text())
    }
  })
  await page.addInitScript(() => {
    document.addEventListener('securitypolicyviolation', (e) => console.error(`CSP violation: ${e.violatedDirective} ${e.blockedURI}`))
    localStorage.setItem('jec.pseudo', '1')
  })
})

test.afterEach(() => {
  expect(violations, 'violations of the Content-Security-Policy').toEqual([])
})

/** problems lists text without markers and text that does not fit. */
/**
 * totp is the code of a base32 key some steps of 30 seconds from now, as
 * authenticator apps make it (RFC 6238).
 */
function totp(key: string, steps: number): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  const bits = [...key.toUpperCase()].map((c) => alphabet.indexOf(c).toString(2).padStart(5, '0')).join('')
  const bytes = Buffer.from((bits.match(/.{8}/g) ?? []).map((b) => parseInt(b, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30_000) + steps))
  const mac = createHmac('sha1', bytes).update(counter).digest()
  const offset = mac[mac.length - 1]! & 0xf
  return String((mac.readUInt32BE(offset) & 0x7fffffff) % 1_000_000).padStart(6, '0')
}

/** problems lists what is wrong with the page as it is now: its text, then what axe finds. */
async function problems(page: Page): Promise<string[]> {
  await page.waitForLoadState('networkidle')
  return [...(await textProblems(page)), ...(await axeProblems(page))]
}

/**
 * settle waits for the animations and transitions under way to end, as a
 * button's turning to its confirming colors: axe would see colors halfway.
 * Endless ones, such as a spinner, are left running.
 */
async function settle(page: Page): Promise<void> {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => Number.isFinite(a.effect?.getComputedTiming().endTime ?? Infinity))
        .map((a) => a.finished.catch(() => undefined)),
    ),
  )
}

/** axeProblems lists the violations axe finds, each with the first elements it is about. */
async function axeProblems(page: Page): Promise<string[]> {
  await settle(page)
  const { violations } = await new AxeBuilder({ page }).analyze()
  return violations.map(
    (v) => `axe ${v.id} (${v.impact ?? 'unknown'}): ${v.help}, in ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(', ')}`,
  )
}

/** textProblems lists text without the markers of the pseudo-locale and text that does not fit. */
async function textProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const found: string[] = []
    // Marked text is whole messages, which may hold other messages as
    // parameters: taking out the innermost ones until none is left leaves
    // nothing but space.
    const marked = (text: string) => {
      let rest = text
      for (let before = ''; rest !== before; ) {
        before = rest
        rest = rest.replace(/⟦[^⟦⟧]*⟧/g, '')
      }
      return rest.trim() === ''
    }
    // Not text for translators: commands, language names (in their own
    // language; the page's own lang is on <html>), data such as domains
    // (translate="no"), dates and numbers that Intl formats (<time>, <data>),
    // and graphics.
    const exempt = (el: Element) => el.closest('code, [lang]:not(html), [translate="no"], time, data, svg, script, style') !== null
    const visible = (el: Element) => {
      const style = getComputedStyle(el)
      return style.display !== 'none' && style.visibility !== 'hidden'
    }
    const where = (el: Element) => `<${el.tagName.toLowerCase()} class="${el.getAttribute('class') ?? ''}">`

    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const text = node.textContent ?? ''
      const el = node.parentElement
      if (!text.trim() || !el || exempt(el) || !visible(el)) continue
      if (!marked(text)) found.push(`text without markers in ${where(el)}: ${text.trim()}`)
    }
    for (const el of document.body.querySelectorAll('[placeholder], [title], [aria-label]')) {
      if (exempt(el)) continue
      for (const attr of ['placeholder', 'title', 'aria-label']) {
        const value = el.getAttribute(attr)
        if (value && !marked(value)) found.push(`${attr} without markers on ${where(el)}: ${value}`)
      }
    }
    for (const el of document.body.querySelectorAll('*')) {
      // Text for screen readers only is clipped on purpose; its markers are checked above.
      if (!el.textContent?.trim() || el.closest('svg, .sr-only') || el.clientWidth === 0) continue
      if (el.scrollWidth <= el.clientWidth + 1) continue
      const style = getComputedStyle(el)
      if (style.overflowX === 'auto' || style.overflowX === 'scroll') continue
      const clipped = style.overflowX === 'hidden' || style.textOverflow === 'ellipsis'
      // Clipped text is fine where a tooltip holds all of it.
      if (clipped && el.getAttribute('title')) continue
      found.push(`${clipped ? 'clipped' : 'overflowing'} text in ${where(el)}: ${el.textContent.trim().slice(0, 80)}`)
    }
    const width = document.documentElement.scrollWidth
    if (width > window.innerWidth + 1) {
      // Name the innermost elements that reach past the window.
      const past = (el: Element) => el.getBoundingClientRect().right > window.innerWidth + 1
      const culprits = [...document.body.querySelectorAll('*')].filter((el) => past(el) && ![...el.children].some(past))
      found.push(
        `the page scrolls sideways: ${width}px wide in a ${window.innerWidth}px window, past it: ${culprits.slice(0, 3).map(where).join(', ')}`,
      )
    }
    return found
  })
}

test('the checks catch untranslated and overflowing text', async ({ page }) => {
  // A page of the app, with its language on <html>. The long words overflow
  // their 40px boxes but stay inside a phone screen.
  await page.setContent(`<!doctype html><html lang="en-US"><body>
    <p>hard-coded</p>
    <p>⟦fine⟧ and raw</p>
    <p>⟦⟦a message⟧ holding ⟦another⟧⟧</p>
    <p lang="de">Deutsch</p>
    <p title="raw tooltip">⟦fine⟧</p>
    <div style="width: 40px; white-space: nowrap">⟦${'w'.repeat(20)}⟧</div>
    <div style="width: 40px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">⟦${'c'.repeat(20)}⟧</div>
    <div style="width: 40px; overflow: hidden; white-space: nowrap" title="⟦all of it⟧">⟦${'t'.repeat(20)}⟧</div>
    <code>jec version</code>
  </body></html>`)
  const found = await textProblems(page)
  expect(found).toEqual([
    'text without markers in <p class="">: hard-coded',
    `text without markers in <p class="">: ⟦fine⟧ and raw`,
    'title without markers on <p class="">: raw tooltip',
    expect.stringMatching(/^overflowing text in <div class="">: ⟦w+/),
    expect.stringMatching(/^clipped text in <div class="">: ⟦c+/),
  ])
})

test('axe catches what keeps a page from being accessible', async ({ page }) => {
  await page.setContent(`<!doctype html><html lang="en-US"><head><title>⟦a page⟧</title></head><body>
    <main><h1>⟦a page⟧</h1><input type="text"></main>
  </body></html>`)
  expect(await axeProblems(page)).toEqual([expect.stringMatching(/^axe label \(critical\): .+, in input$/)])
})

test('the app installs to a home screen', async ({ page }) => {
  // The manifest the page links, under the base path, and each of its icons.
  await page.goto('./')
  const href = await page.evaluate(() => document.querySelector<HTMLLinkElement>('link[rel="manifest"]')?.href ?? '')
  const res = await page.request.get(href)
  expect(res.ok()).toBe(true)
  expect(res.headers()['content-type']).toBe('application/manifest+json')
  const manifest = (await res.json()) as { start_url: string; icons: { src: string; sizes: string; purpose?: string }[] }
  expect(manifest.icons.map((i) => i.sizes)).toEqual(expect.arrayContaining(['192x192', '512x512']))
  expect(manifest.icons.some((i) => i.purpose === 'maskable')).toBe(true)
  for (const icon of manifest.icons) {
    expect((await page.request.get(new URL(icon.src, href).toString())).ok(), icon.src).toBe(true)
  }
  expect(new URL(manifest.start_url, href).toString()).toBe(new URL('./', href).toString())
})

// Soft checks: one run reports the problems of every page, not just the first.
test('every page is translated and fits', async ({ page }, info) => {
  // A walk through every page, each also through axe: it outgrows the
  // default 30 seconds on CI's runners.
  test.setTimeout(240_000)
  const token = process.env[`JEC_E2E_TOKEN_${info.project.name.toUpperCase()}`] ?? ''

  // A fresh server sends visitors to the setup wizard.
  await page.goto('./')
  await expect(page).toHaveURL(/\/setup$/)
  expect.soft(await problems(page), 'setup wizard').toEqual([])

  // Its own check: the two passwords differ.
  await page.locator('#password').fill('correct horse')
  await page.locator('#again').fill('correct hors')
  await page.locator('form button[type=submit]').click()
  await expect(page.locator('#again-error')).toBeVisible()
  expect.soft(await problems(page), 'setup wizard with an error').toEqual([])

  // The server's errors come back as message keys.
  await page.locator('#token').fill('wrong token')
  await page.locator('#username').fill('admin')
  await page.locator('#again').fill('correct horse')
  await page.locator('form button[type=submit]').click()
  await expect(page.getByRole('alert')).toBeVisible()
  expect.soft(await problems(page), 'setup wizard with a server error').toEqual([])

  await page.locator('#token').fill(token)
  await page.locator('form button[type=submit]').click()
  await expect(page).toHaveURL(/\/$/)
  expect.soft(await problems(page), 'overview without certificates').toEqual([])

  await page.goto('./certificates/new')
  expect.soft(await problems(page), 'add a certificate').toEqual([])
  await page.locator('#certificate').fill('not a certificate')
  await page.locator('button[form=add-form]').click()
  await expect(page.locator('#certificate-error')).toBeVisible()
  expect.soft(await problems(page), 'add a certificate with an error').toEqual([])
  // As a PKCS #12 file, which this is not.
  await page.getByRole('button', { name: label('certs.add.format_pfx') }).click()
  await page.locator('#pfx').setInputFiles({ name: 'cert.pfx', mimeType: 'application/x-pkcs12', buffer: Buffer.from('not a PFX file') })
  await page.locator('button[form=add-form]').click()
  await expect(page.locator('#pfx-error')).toBeVisible()
  expect.soft(await problems(page), 'add a certificate from a PFX file').toEqual([])
  await page.getByRole('button', { name: label('certs.add.format_pem') }).click()

  // A certificate in each state, the first two with long and several names.
  for (const [names, from, to] of [
    [['shop.example.com', 'www.shop.example.com'], -177, 23],
    [['a-rather-long-subdomain-name.example.com'], -85, 5],
    [['old.example.com'], -92, -2],
    [['docs.example.org'], -38, 52],
    [['blog.example.com'], -69, 131],
  ] as const) {
    const res = await page.request.post('api/v1/certs/certificates', { data: await selfSigned([...names], from, to) })
    expect(res.status(), names[0]).toBe(201)
  }
  await page.goto('./')
  expect.soft(await problems(page), 'overview').toEqual([])
  await page.goto('./certificates')
  expect.soft(await problems(page), 'certificates').toEqual([])
  if (info.project.name === 'desktop') {
    // The menu of a row, which phones go without; then deletion from it,
    // left unconfirmed.
    const more = page.getByRole('button', { name: message('certs.menu.label', { domain: 'old.example.com' }) })
    await more.click()
    const rowMenu = page.getByRole('menu')
    await expect(rowMenu.getByRole('menuitem').first()).toBeFocused()
    expect.soft(await problems(page), 'the menu of a certificate').toEqual([])
    await page.keyboard.press('Escape')
    await expect(rowMenu).toBeHidden()
    await expect(more).toBeFocused()
    await more.click()
    await rowMenu.getByRole('menuitem', { name: label('certs.action.delete') }).click()
    await expect(page.locator('dialog[open]')).toBeVisible()
    expect.soft(await problems(page), 'delete a certificate from the list').toEqual([])
    await page.locator('dialog[open]').getByRole('button', { name: label('kernel.action.cancel') }).click()
    await expect(page.locator('dialog[open]')).toHaveCount(0)
  }
  await page.goto('./certificates/1')
  expect.soft(await problems(page), 'a certificate').toEqual([])
  // Its CSR, and its renewal settings, each in a dialog.
  await page.getByRole('button', { name: label('certs.csr.show') }).click()
  await expect(page.locator('dialog[open] pre')).toBeVisible()
  expect.soft(await problems(page), 'the CSR of a certificate').toEqual([])
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.getByRole('button', { name: label('certs.settings.edit') }).click()
  await expect(page.locator('dialog[open] #renewBeforeDays')).toBeVisible()
  expect.soft(await problems(page), 'the renewal settings of a certificate').toEqual([])
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[open]')).toHaveCount(0)

  await page.goto('./certificates/1?upload=1')
  await expect(page.locator('dialog[open]')).toBeVisible()
  expect.soft(await problems(page), 'upload a new certificate').toEqual([])
  await page.locator('#certificate').fill('not a certificate')
  await page.locator('button[form=upload-form]').click()
  await expect(page.locator('#certificate-error')).toBeVisible()
  expect.soft(await problems(page), 'upload a new certificate with an error').toEqual([])
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[open]')).toHaveCount(0)

  await page.getByRole('button', { name: label('certs.action.download') }).filter({ visible: true }).first().click()
  await expect(page.locator('dialog[open]')).toBeVisible()
  expect.soft(await problems(page), 'download a certificate').toEqual([])
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[open]')).toHaveCount(0)

  await page.getByRole('button', { name: label('certs.action.delete') }).filter({ visible: true }).first().click()
  await expect(page.locator('dialog[open]')).toBeVisible()
  expect.soft(await problems(page), 'delete a certificate').toEqual([])
  await page.keyboard.press('Escape')

  // Certificate sources: an ACME account at a CA that is not there, so
  // that issuing fails at once and nothing leaves this machine.
  await page.goto('./sources')
  expect.soft(await problems(page), 'certificate sources').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.source.add') }).filter({ visible: true }).first().click()
  const dialog = page.locator('dialog[open]')
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'add a source').toEqual([])
  await pick(dialog, label('acme.account.name'))
  await dialog.getByLabel(label('acme.field.ca')).selectOption('custom')
  await dialog.locator('button[type=submit]').click()
  await expect(dialog.getByText(label('kernel.error.field_required'))).toBeVisible()
  expect.soft(await problems(page), 'add a source with an error').toEqual([])
  await dialog.getByLabel(label('acme.field.directory')).fill('https://127.0.0.1:9/directory')
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  expect.soft(await problems(page), 'certificate sources with one').toEqual([])

  // DNS providers: the choice among lego's, then the form of one.
  await page.goto('./dns')
  expect.soft(await problems(page), 'domain validation').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.solver.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'add a DNS provider').toEqual([])
  await dialog.getByRole('searchbox').fill('http')
  await pick(dialog, 'HTTP request')
  await expect(dialog.getByLabel('HTTPREQ_ENDPOINT')).toBeVisible()
  expect.soft(await problems(page), 'add a DNS provider: its form').toEqual([])
  await dialog.getByLabel('HTTPREQ_ENDPOINT').fill('http://127.0.0.1:9')
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  expect.soft(await problems(page), 'domain validation with one').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.solver.add') }).filter({ visible: true }).first().click()
  await dialog.getByRole('searchbox').fill('wellknown')
  await pick(dialog, label('wellknown.answer.name'))
  await expect(dialog.getByLabel(label('wellknown.field.domain'))).toBeVisible()
  expect.soft(await problems(page), 'add a validation answered by JEC').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.getByRole('button', { name: label('kernel.providers.solver.add') }).filter({ visible: true }).first().click()
  await dialog.getByRole('searchbox').fill('esa.edge')
  await pick(dialog, label('esa.edge.name'))
  await expect(dialog.getByLabel(label('esa.field.domain'))).toBeVisible()
  expect.soft(await problems(page), "add a validation answered at ESA's edge").toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)

  // Deploy targets: ESA's form, then a local directory, which needs no
  // network.
  await page.goto('./targets')
  expect.soft(await problems(page), 'deploy targets').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.target.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'add a deploy target').toEqual([])
  await pick(dialog, label('esa.site.name'))
  await expect(dialog.getByLabel(label('esa.field.site'))).toBeVisible()
  expect.soft(await problems(page), 'add a deploy target: ESA').toEqual([])
  for (const [type, field] of [
    ['ssh.server.name', 'ssh.field.host_key'],
    ['webhook.deploy.name', 'webhook.field.secret'],
  ] as const) {
    if (info.project.name === 'phone') await dialog.getByRole('button', { name: label('kernel.providers.choose_other') }).click()
    await pick(dialog, label(type))
    await expect(dialog.getByLabel(label(field))).toBeVisible()
    expect.soft(await problems(page), `add a deploy target: ${type}`).toEqual([])
  }
  // A plugin's type, with the texts it brings, where shell scripts run.
  if (process.platform !== 'win32') {
    if (info.project.name === 'phone') await dialog.getByRole('button', { name: label('kernel.providers.choose_other') }).click()
    await pick(dialog, pseudoize('E2E plugin target'))
    await expect(dialog.getByLabel(pseudoize('Endpoint'))).toBeVisible()
    await expect(dialog.getByText(pseudoize('Where it {would} deploy'))).toBeVisible()
    expect.soft(await problems(page), 'add a deploy target: a plugin').toEqual([])
  }
  if (info.project.name === 'phone') await dialog.getByRole('button', { name: label('kernel.providers.choose_other') }).click()
  await pick(dialog, label('files.directory.name'))
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e files')
  await dialog.getByLabel(label('files.field.directory')).fill(`${process.env.JEC_E2E_FILES}/${info.project.name}`)
  expect.soft(await problems(page), 'add a deploy target: a directory').toEqual([])
  // Its advanced settings: the names of the files, a PFX file among them.
  await dialog.locator('details > summary').click()
  await dialog.getByLabel(label('certfiles.field.pfx'), { exact: true }).fill('{domain}.pfx')
  expect.soft(await problems(page), 'add a deploy target: a directory, with its files').toEqual([])
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  expect.soft(await problems(page), 'deploy targets with one').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.test') }).click()
  await expect(page.getByRole('status')).toBeVisible()
  expect.soft(await problems(page), 'deploy targets: a test that passed').toEqual([])

  // A note on a certificate: the dialog, then the page with it.
  await page.goto('./certificates/1')
  await page.getByRole('button', { name: label('certs.memo.edit') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'the note of a certificate').toEqual([])
  await dialog.locator('#note').fill('e2e note')
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await expect(page.getByText('e2e note')).toBeVisible()
  expect.soft(await problems(page), 'a certificate with a note').toEqual([])

  // An uploaded certificate deployed there.
  await page.goto('./certificates/1')
  await page.getByRole('button', { name: label('certs.action.choose_targets') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'choose targets').toEqual([])
  await dialog.getByRole('checkbox').first().check()
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await expect(async () => {
    await page.reload()
    await expect(page.getByText(label('certs.deployment.state.done')).first()).toBeVisible({ timeout: 1000 })
  }).toPass({ timeout: 20_000 })
  expect.soft(await problems(page), 'a certificate deployed').toEqual([])
  // Deploying it there again asks first.
  await page.getByRole('button', { name: message('certs.action.redeploy_to', { target: 'e2e files' }) }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'deploy again, asked first').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.getByRole('link', { name: /e2e files/ }).click()
  await expect(page).toHaveURL(/\/deployments\/\d+$/)
  await expect(page.getByText(label('certs.deployment.done')).first()).toBeVisible()
  expect.soft(await problems(page), 'a deployment').toEqual([])

  // That directory as a source, as certbot's would be: its test finds the
  // certificate deployed there, with its key.
  await page.goto('./sources')
  await page.getByRole('button', { name: label('kernel.providers.source.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  await dialog.getByRole('searchbox').fill('files.watch')
  await pick(dialog, label('files.watch.name'))
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e watch')
  await dialog.getByLabel(label('files.field.watch_directory')).fill(`${process.env.JEC_E2E_FILES}/${info.project.name}`)
  expect.soft(await problems(page), 'add a source: a directory to watch').toEqual([])
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  const watched = page.getByRole('article').filter({ hasText: 'e2e watch' })
  await watched.getByRole('button', { name: label('kernel.providers.test') }).click()
  await expect(watched.getByRole('status')).toBeVisible()
  expect.soft(await problems(page), 'sources: a directory watched').toEqual([])

  // A directory given as a relative path, which fails for good: the second
  // certificate cannot be deployed.
  await page.goto('./targets')
  await page.getByRole('button', { name: label('kernel.providers.target.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  await pick(dialog, label('files.directory.name'))
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e relative')
  await dialog.getByLabel(label('files.field.directory')).fill('relative/certs')
  // Saved and tested at once: the dialog stays, with what the test said.
  await dialog.getByRole('button', { name: label('kernel.providers.save_test') }).click()
  await expect(dialog.getByRole('alert')).toBeVisible()
  expect.soft(await problems(page), 'add a deploy target: saved, and a test that failed').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.getByRole('article').filter({ hasText: 'e2e relative' }).getByRole('button', { name: label('kernel.providers.test') }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  expect.soft(await problems(page), 'deploy targets: a test that failed').toEqual([])
  await page.goto('./certificates/2')
  await page.getByRole('button', { name: label('certs.action.choose_targets') }).click()
  await expect(dialog).toBeVisible()
  await dialog.getByRole('checkbox', { name: /e2e relative/ }).check()
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await expect(async () => {
    await page.reload()
    await expect(page.getByText(label('certs.deployment.state.failed')).first()).toBeVisible({ timeout: 1000 })
  }).toPass({ timeout: 20_000 })
  expect.soft(await problems(page), 'a certificate that could not be deployed').toEqual([])
  await page.getByRole('link', { name: /e2e relative/ }).click()
  await expect(page).toHaveURL(/\/deployments\/\d+$/)
  expect.soft(await problems(page), 'a deployment that failed').toEqual([])
  await page.goto('./certificates')
  expect.soft(await problems(page), 'certificates with one that could not be deployed').toEqual([])
  // The target's card tells how it fared last: the deployment, after the test.
  await page.goto('./targets')
  const relative = page.getByRole('article').filter({ hasText: 'e2e relative' })
  await expect(relative.locator('p.text-warn-ink time')).toBeVisible()
  expect.soft(await problems(page), 'deploy targets, one of which failed').toEqual([])

  // Notifications: Telegram's form, then a webhook to this machine, whose
  // test the guard against private addresses refuses.
  await page.goto('./notify')
  expect.soft(await problems(page), 'notifications').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.notifier.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'add a channel').toEqual([])
  await pick(dialog, label('telegram.bot.name'))
  await expect(dialog.getByLabel(label('telegram.field.chat'))).toBeVisible()
  expect.soft(await problems(page), 'add a channel: Telegram').toEqual([])
  if (info.project.name === 'phone') await dialog.getByRole('button', { name: label('kernel.providers.choose_other') }).click()
  await pick(dialog, label('webhook.notify.name'))
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e hook')
  await dialog.getByLabel(label('webhook.field.url')).fill('http://127.0.0.1:9/hook')
  await dialog.locator('details > summary').click()
  await expect(dialog.getByLabel(label('webhook.field.body'))).toBeVisible()
  expect.soft(await problems(page), 'add a channel: a webhook, with its advanced settings').toEqual([])
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  expect.soft(await problems(page), 'notifications with one').toEqual([])
  await page.getByRole('button', { name: label('notify.action.test') }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  expect.soft(await problems(page), 'a test that failed').toEqual([])

  // A script, whose test fails with what it wrote.
  await page.getByRole('button', { name: label('kernel.providers.notifier.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  await pick(dialog, label('hooks.script.name'))
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e script')
  await dialog.getByLabel(label('hooks.field.script')).fill('echo "no route from $JEC_EVENT" >&2; exit 1')
  expect.soft(await problems(page), 'add a channel: a script').toEqual([])
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  const script = page.locator('article', { hasText: 'e2e script' })
  await script.getByRole('button', { name: label('notify.action.test') }).click()
  await expect(script.getByRole('alert')).toHaveText(message('hooks.error.status_output', { status: '1', output: 'no route from notify.test' }))
  expect.soft(await problems(page), 'a script whose test failed').toEqual([])
  // The calendar feed, turned on: its secret URL shows.
  await page.getByRole('switch', { name: label('calendar.page.title') }).click()
  await expect(page.getByRole('link', { name: label('calendar.page.subscribe') })).toBeVisible()
  expect.soft(await problems(page), 'the calendar feed').toEqual([])

  // Site watch: the page, a site added that does not answer, and the dialog
  // that stops watching it.
  await page.goto('./sites')
  expect.soft(await problems(page), 'site watch').toEqual([])
  await page.getByRole('button', { name: label('watch.page.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'watch a site').toEqual([])
  await dialog.getByLabel(label('watch.field.host')).fill('127.0.0.1:9')
  await dialog.getByLabel(label('watch.field.name')).fill('e2e site')
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await expect(page.getByText(label('watch.state.unreachable')).filter({ visible: true }).first()).toBeVisible()
  expect.soft(await problems(page), 'a site that does not answer').toEqual([])
  await page.getByRole('button', { name: message('watch.page.delete', { site: '127.0.0.1:9' }) }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'stop watching a site').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()

  // Domain registrations: no lookup reaches a registry here, so the list is
  // made up, with each kind of row; a name with no registration is refused.
  const later = (days: number) => new Date(Date.now() + days * 86_400_000).toISOString()
  await page.route('**/api/v1/watch/domains', (route) =>
    route.request().method() !== 'GET'
      ? route.continue()
      : route.fulfill({
          json: {
            domains: [
              {
                name: 'e2e-shop.com', manual: true, certificates: [], sites: [], state: 'expiring', expiresAt: later(12),
                registrar: 'Example Registrar, Inc.', checkedAt: later(0), checkAt: later(0.5),
              },
              {
                name: 'e2e-shop.de', manual: false, certificates: [], sites: [1], state: 'unknown',
                error: 'the registry of this TLD has no RDAP service', checkedAt: later(0), checkAt: later(7),
              },
            ],
          },
        }),
  )
  await page.reload()
  await expect(page.getByText('e2e-shop.de').filter({ visible: true })).toBeVisible()
  expect.soft(await problems(page), 'domain registrations').toEqual([])
  await page.getByRole('button', { name: message('watch.domain.delete', { domain: 'e2e-shop.com' }) }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'stop watching a domain').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await page.getByRole('button', { name: label('watch.domain.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  await dialog.getByLabel(label('watch.domain.field.name')).fill('nas.lan')
  await dialog.locator('button[type=submit]').click()
  await expect(dialog.getByText(label('watch.error.domain'))).toBeVisible()
  expect.soft(await problems(page), 'a domain with no registration').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await page.unroute('**/api/v1/watch/domains')

  // Credentials: the page, a new one, and a provider that takes its keys
  // from it, whose fields for them then go.
  await page.goto('./credentials')
  expect.soft(await problems(page), 'credentials').toEqual([])
  await page.getByRole('button', { name: label('kernel.credentials.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'add a credential').toEqual([])
  await pick(dialog, label('credentials.ssh_key.name'))
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e key')
  await dialog.getByLabel(label('credentials.ssh_key.private_key')).fill('e2e private key')
  expect.soft(await problems(page), 'add a credential: an SSH key').toEqual([])
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'e2e key' })).toBeVisible()
  expect.soft(await problems(page), 'credentials with one').toEqual([])
  await page.goto('./targets')
  await page.getByRole('button', { name: label('kernel.providers.target.add') }).filter({ visible: true }).first().click()
  await pick(dialog, label('ssh.server.name'))
  await expect(dialog.getByLabel(label('ssh.field.private_key'))).toBeVisible()
  await dialog.getByLabel(label('kernel.providers.credential.label')).selectOption({ label: 'e2e key' })
  await expect(dialog.getByLabel(label('ssh.field.private_key'))).toBeHidden()
  expect.soft(await problems(page), 'a target with a credential').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(dialog).toBeHidden()

  // A certificate from the source: the form, its errors, the issuance.
  await page.goto('./certificates/new')
  await expect(page.locator('#domain-input')).toBeVisible()
  expect.soft(await problems(page), 'add a certificate from a source').toEqual([])
  await page.locator('button[form=add-form]').click()
  await expect(page.locator('#domains-help')).toHaveText(label('kernel.error.invalid_field'))
  expect.soft(await problems(page), 'add a certificate from a source with an error').toEqual([])
  // Several at once, a line each: the box, then back to one.
  await page.getByRole('button', { name: label('certs.add.bulk_many') }).click()
  await page.locator('#bulk-input').fill('a.example.com www.a.example.com\nb.example.com')
  expect.soft(await problems(page), 'add several certificates').toEqual([])
  await page.getByRole('button', { name: label('certs.add.bulk_one') }).click()
  await page.locator('#domain-input').fill('issued.example.com')
  await page.locator('#domain-input').press('Enter')
  await page.locator('#domain-input').fill('*.issued.example.com')
  await page.locator('#domain-input').press('Enter')
  await page.locator('button[form=add-form]').click()
  await expect(page).toHaveURL(/\/certificates\/\d+\/issuances\/\d+$/)
  await expect(page.getByText(label('certs.issuance.next_try')).first()).toBeVisible() // the first attempt failed
  expect.soft(await problems(page), 'an issuance that failed').toEqual([])
  // No line of its log has raw details, so the log offers none.
  await expect(page.getByRole('checkbox', { name: label('certs.issuance.show_detail') })).toHaveCount(0)
  await page.getByRole('link', { name: label('certs.action.details') }).filter({ visible: true }).first().click()
  await expect(page).toHaveURL(/\/certificates\/\d+$/)
  await expect(page.getByRole('heading', { name: label('certs.detail.last_issuance') })).toBeVisible()
  expect.soft(await problems(page), 'a certificate whose issuance failed').toEqual([])

  // Revoking. There is no CA to have issued versions, so the page is shown
  // made-up ones: the one in use, one it can revoke and one revoked. The
  // server knows none of them, and refuses.
  const issuedId = /\/certificates\/(\d+)$/.exec(page.url())?.[1] ?? ''
  const day = 24 * 3600 * 1000
  const at = (days: number) => new Date(Date.now() + days * day).toISOString()
  const madeUp = (id: number, from: number, to: number, extra: Record<string, unknown>) => ({
    id,
    origin: 'issued',
    names: ['issued.example.com', '*.issued.example.com'],
    serial: id.toString(16),
    fingerprint: id.toString(16).padStart(64, '0'),
    issuer: 'Fake CA',
    keyType: 'ecdsa-p256',
    notBefore: at(from),
    notAfter: at(to),
    createdAt: at(from),
    ...extra,
  })
  const detailRoute = `**/api/v1/certs/certificates/${issuedId}`
  await page.route(detailRoute, async (route) => {
    const res = await route.fetch()
    const body = (await res.json()) as { certificate: Record<string, unknown>; versions: unknown[] }
    // The current one its CA revoked, as OCSP or the CRL told.
    const versions = [
      madeUp(9003, -10, 80, { caRevokedAt: at(-1), caRevokeReason: 'superseded' }),
      madeUp(9002, -40, 50, { revocable: true }),
      madeUp(9001, -70, 20, { revokedAt: at(-40), revokeReason: 'key_compromise' }),
    ]
    body.certificate.current = versions[0]
    body.certificate.state = 'revoked'
    body.versions = versions
    await route.fulfill({ response: res, json: body })
  })
  await page.reload()
  const versionRows = page.locator('section[aria-labelledby=versions] li')
  await expect(versionRows).toHaveCount(3)
  await expect(page.getByText(label('certs.detail.revoked_renewing'))).toBeVisible()
  expect.soft(await problems(page), 'versions, one revocable and one revoked').toEqual([])
  await versionRows.nth(1).getByRole('button').first().click()
  await expect(page.locator('dialog[open]')).toBeVisible()
  expect.soft(await problems(page), 'revoke a version').toEqual([])
  await page.locator('#revoke-password').fill('correct horse')
  await page.locator('button[form=revoke-form]').click()
  await expect(page.locator('dialog[open]').getByRole('alert')).toBeVisible()
  expect.soft(await problems(page), 'revoke a version, refused').toEqual([])
  await page.keyboard.press('Escape')
  await page.unroute(detailRoute)

  await page.goto('./certificates')
  expect.soft(await problems(page), 'certificates with a failed one').toEqual([])
  await page.goto('./')
  expect.soft(await problems(page), 'overview with a failed issuance').toEqual([])

  // The history of all that, then filtered down to nothing.
  await page.goto('./history')
  await expect(page.getByText(label('certs.runs.result.failed')).first()).toBeVisible()
  expect.soft(await problems(page), 'history').toEqual([])
  await page.getByRole('searchbox').fill('nothing.example')
  await expect(page.getByText(label('certs.runs.no_match'))).toBeVisible()
  expect.soft(await problems(page), 'history with no match').toEqual([])

  // The modules: one switched off and on again, then a search that finds none.
  await page.goto('./modules')
  const first = page.locator('button[role=switch][aria-checked=true]:not([disabled])').first()
  await expect(first).toBeVisible()
  expect.soft(await problems(page), 'modules').toEqual([])
  const module = page.getByRole('switch', { name: (await first.getAttribute('aria-label')) ?? '', exact: true })
  await module.click()
  await expect(module).toHaveAttribute('aria-checked', 'false')
  await page.getByRole('button', { name: label('kernel.modules.filter.off') }).click()
  expect.soft(await problems(page), 'modules switched off').toEqual([])
  await module.click()
  await expect(module).toHaveCount(0) // on again, so no longer among those off
  await page.getByRole('button', { name: label('kernel.modules.filter.all') }).click()
  // The plugins JEC found.
  await expect(page.getByRole('heading', { name: label('plugins.page.title') })).toBeVisible()
  if (process.platform !== 'win32') await expect(page.getByText(pseudoize('E2E plugin'), { exact: true })).toBeVisible()
  expect.soft(await problems(page), 'modules with the plugins').toEqual([])
  await page.getByRole('searchbox').fill('nothing.example')
  await expect(page.getByText(label('kernel.modules.no_match'))).toBeVisible()
  expect.soft(await problems(page), 'modules with no match').toEqual([])

  // An experimental module, switched on, brings its type, marked as such.
  await page.getByRole('searchbox').fill('')
  const namecheap = page.locator('li', { hasText: label('namecheap.module.name') }).getByRole('switch')
  await namecheap.click()
  await expect(namecheap).toHaveAttribute('aria-checked', 'true')
  await page.goto('./sources')
  await page.getByRole('button', { name: label('kernel.providers.source.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  await pick(dialog, label('namecheap.ssl_manager.name'))
  await expect(dialog.getByLabel(label('namecheap.field.token'))).toBeVisible()
  expect.soft(await problems(page), 'add a source: Namecheap').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()

  // With the notify module off, the notifications say that none go out.
  await page.goto('./modules')
  const notify = page.locator('li', { hasText: label('notify.module.name') }).getByRole('switch')
  await notify.click()
  await expect(notify).toHaveAttribute('aria-checked', 'false')
  await page.goto('./notify')
  await expect(page.getByText(label('kernel.providers.notifier.dispatch_off'))).toBeVisible()
  expect.soft(await problems(page), 'notifications with the notify module off').toEqual([])
  await page.goto('./modules')
  await notify.click()
  await expect(notify).toHaveAttribute('aria-checked', 'true')

  // The settings: a default saved at once, then one the server refuses.
  await page.goto('./settings')
  const days = page.locator('#renewBeforeDays')
  await expect(days).toBeVisible()
  expect.soft(await problems(page), 'settings').toEqual([])
  // The background jobs, made up to have each kind, and little space left.
  const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()
  await page.route('**/api/v1/system/health', (route) =>
    route.fulfill({
      json: {
        jobs: [
          { name: 'certs.renew', module: 'certs', everySeconds: 3600, running: false, startedAt: ago(5), finishedAt: ago(4) },
          {
            name: 'watch.domains', module: 'watch', everySeconds: 600, running: false, startedAt: ago(9), finishedAt: ago(8),
            error: 'Get "https://rdap.example/domain/e2e-shop.com": dial tcp: lookup rdap.example: no such host',
          },
          { name: 'monitor.check', module: 'monitor', everySeconds: 60, running: true, startedAt: ago(0) },
          { name: 'calendar.never', module: 'calendar', everySeconds: 86400, running: false },
        ],
        disk: { free: 512 * 1024 ** 2, total: 20 * 1024 ** 3 },
      },
    }),
  )
  await page.reload()
  await page.getByRole('button', { name: label('kernel.health.details') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'background jobs').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.close') }).last().click()
  await page.unroute('**/api/v1/system/health')
  // The dark theme, chosen in this browser, then the system's again.
  await page.getByRole('button', { name: label('kernel.settings.theme_dark') }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  expect.soft(await problems(page), 'settings in the dark theme').toEqual([])
  await page.getByRole('button', { name: label('kernel.settings.theme_system') }).click()
  await expect(page.getByRole('button', { name: label('kernel.settings.theme_system') })).toHaveAttribute('aria-pressed', 'true')
  // The time zone of the panel, chosen and then the server's again.
  const zone = page.locator('#timeZone')
  await zone.selectOption('Asia/Shanghai')
  const appearance = page.getByRole('region', { name: label('kernel.settings.appearance') })
  await expect(appearance.getByRole('status').filter({ hasText: label('kernel.settings.saved') })).toBeVisible()
  expect.soft(await problems(page), 'settings with a time zone chosen').toEqual([])
  await zone.selectOption('')
  await expect(zone).toHaveValue('')
  await days.fill('45')
  await days.blur()
  const renewal = page.getByRole('region', { name: label('certs.defaults.title') })
  await expect(renewal.getByRole('status').filter({ hasText: label('kernel.settings.saved') })).toBeVisible()
  expect.soft(await problems(page), 'settings saved').toEqual([])
  await days.fill('0')
  await days.blur()
  await expect(days).toHaveAttribute('aria-invalid', 'true')
  expect.soft(await problems(page), 'settings refused').toEqual([])
  await days.fill('30')
  await days.blur()
  await expect(days).not.toHaveAttribute('aria-invalid')
  // More retries than the server allows, then the default again.
  const retries = page.locator('#retries')
  await retries.fill('25')
  await retries.blur()
  await expect(retries).toHaveAttribute('aria-invalid', 'true')
  expect.soft(await problems(page), 'too many retries').toEqual([])
  await retries.fill('7')
  await retries.blur()
  await expect(retries).not.toHaveAttribute('aria-invalid')
  // Changing the password: two new passwords that differ stop it before the
  // server, where a wrong current password would count toward the lockout.
  await page.getByRole('button', { name: label('kernel.settings.change_password') }).click()
  await expect(dialog).toBeVisible()
  await dialog.locator('#current-password').fill('correct horse')
  await dialog.locator('#new-password').fill('a new password')
  await dialog.locator('#password-again').fill('another password')
  await dialog.locator('button[type=submit]').click()
  await expect(dialog.getByText(label('kernel.account.password_mismatch'))).toBeVisible()
  expect.soft(await problems(page), 'change the password').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  // A backup, downloaded once the password is confirmed.
  await page.getByRole('button', { name: label('kernel.settings.backup_download') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'download a backup').toEqual([])
  await dialog.locator('#backup-password').fill('correct horse')
  const download = page.waitForEvent('download')
  await dialog.locator('button[type=submit]').click()
  expect((await download).suggestedFilename()).toMatch(/^jec-backup-\d{8}-\d{6}\.db$/)
  await expect(page.locator('dialog[open]')).toHaveCount(0)

  // The configuration as YAML: exported, and with its secrets once the
  // password is confirmed; imported, first a file with problems, then one
  // that changes a default.
  const exported = page.waitForEvent('download')
  await page.getByRole('button', { name: label('kernel.settings.config_export'), exact: true }).click()
  const config = readFileSync(await (await exported).path(), 'utf8')
  expect(config).toMatch(/^version: 1\n/)
  await page.getByRole('button', { name: label('kernel.settings.config_export_secrets') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'export the configuration with secrets').toEqual([])
  await dialog.locator('#config-export-password').fill('correct horse')
  const withSecrets = page.waitForEvent('download')
  await dialog.locator('button[type=submit]').click()
  expect((await withSecrets).suggestedFilename()).toMatch(/^jec-config-\d{8}-\d{6}\.yaml$/)
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.getByRole('button', { name: label('kernel.settings.config_import') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'import a configuration').toEqual([])
  const broken = 'version: 1\nrenewal:\n  check_time: "25:00"\ncertificates:\n  - {domains: [new.example.com], source: Nowhere, targets: [Nowhere]}\n  - {domains: [nosource.example.com]}\n'
  await dialog.locator('#config-file').setInputFiles({ name: 'broken.yaml', mimeType: 'application/yaml', buffer: Buffer.from(broken) })
  await expect(dialog.getByText(label('kernel.config.action.skip')).first()).toBeVisible()
  await expect(dialog.getByRole('button', { name: label('kernel.config.apply') })).toBeDisabled()
  expect.soft(await problems(page), 'import a configuration with problems').toEqual([])
  const changed = config.replace(/check_time: "?\d\d:\d\d"?/, 'check_time: "04:15"')
  await dialog.locator('#config-file').setInputFiles({ name: 'jec-config.yaml', mimeType: 'application/yaml', buffer: Buffer.from(changed) })
  await expect(dialog.getByText(label('kernel.config.action.change')).first()).toBeVisible()
  expect.soft(await problems(page), 'import a configuration: what it changes').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.config.apply') }).click()
  await expect(page.getByRole('status').filter({ hasText: label('kernel.settings.config_imported') })).toBeVisible()
  await expect(page.locator('#checkTime')).toHaveValue('04:15')
  expect.soft(await problems(page), 'configuration imported').toEqual([])

  // Two-factor authentication, on with a code of the new key, which the
  // test reckons as an authenticator app does.
  await page.getByRole('button', { name: label('kernel.settings.two_factor_turn_on') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'turn on two-factor authentication').toEqual([])
  await dialog.locator('#two-factor-password').fill('correct horse')
  await dialog.locator('button[type=submit]').click()
  await expect(dialog.locator('#two-factor-code')).toBeVisible()
  expect.soft(await problems(page), 'turn on two-factor authentication: the key').toEqual([])
  const key = ((await dialog.locator('code').first().textContent()) ?? '').replaceAll(' ', '')
  await dialog.locator('#two-factor-code').fill(totp(key, 0))
  await dialog.locator('button[type=submit]').click()
  await expect(page.getByRole('status').filter({ hasText: label('kernel.settings.two_factor_done') })).toBeVisible()
  expect.soft(await problems(page), 'two-factor authentication on').toEqual([])
  await page.locator('#session-days').selectOption('30')

  // Restoring a backup: the dialog, with a file chosen, then let be.
  await page.getByRole('button', { name: label('kernel.settings.restore') }).click()
  await expect(dialog).toBeVisible()
  await dialog.locator('#restore-file').setInputFiles({ name: 'jec-backup-20261018-120000.db', mimeType: 'application/octet-stream', buffer: Buffer.from('SQLite format 3') })
  await dialog.locator('#restore-password').fill('correct horse')
  expect.soft(await problems(page), 'restore a backup').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(dialog).toBeHidden()

  // API tokens: one made, shown once, then deleted at a second click.
  await page.getByRole('button', { name: label('kernel.settings.tokens_manage') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'API tokens').toEqual([])
  await dialog.locator('#token-name').fill('ci')
  await dialog.locator('#token-scope').selectOption('write')
  await dialog.locator('#token-password').fill('correct horse')
  await dialog.locator('button[type=submit]').click()
  await expect(dialog.locator('code').filter({ hasText: 'jec_' })).toBeVisible()
  expect.soft(await problems(page), 'API tokens: a new one').toEqual([])
  const remove = dialog.getByRole('button', { name: pseudoize('Delete ci') })
  await remove.click()
  expect.soft(await problems(page), 'API tokens: delete, to confirm').toEqual([])
  await remove.click()
  await expect(dialog.getByText(label('kernel.tokens.empty'))).toBeVisible()
  await dialog.getByRole('button', { name: label('kernel.action.close') }).last().click()
  await expect(dialog).toBeHidden()
  await expect(page.locator('#session-days')).toHaveValue('30')

  // Single sign-on: switched on with a provider that is not there, which
  // the server refuses, field by field.
  await page.getByRole('button', { name: label('oidc.settings.edit') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'single sign-on').toEqual([])
  await dialog.locator('#oidc-issuer').fill('http://127.0.0.1:9/nowhere')
  await dialog.locator('#oidc-client').fill('jec')
  await dialog.locator('#oidc-allowed').fill('admin@example.com')
  await dialog.locator('button[type=submit]').click()
  await expect(dialog.getByText(label('oidc.error.discovery'))).toBeVisible()
  expect.soft(await problems(page), 'single sign-on refused').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(dialog).toBeHidden()

  await page.goto('./more')
  expect.soft(await problems(page), 'more').toEqual([])
  await page.getByRole('button', { name: label('kernel.shell.sign_out') }).filter({ visible: true }).first().click()
  await expect(page).toHaveURL(/\/login$/)
  expect.soft(await problems(page), 'sign-in page').toEqual([])
  // Back from a single sign-on the provider refused.
  await page.goto('./login?sso=denied')
  await expect(page.getByText(label('oidc.login.denied'))).toBeVisible()
  expect.soft(await problems(page), 'sign-in page, after a refused single sign-on').toEqual([])
  await page.goto('./login')

  await page.locator('#username').fill('admin')
  await page.locator('#password').fill('wrong horse')
  await page.locator('form button[type=submit]').click()
  await expect(page.getByRole('alert')).toBeVisible()
  expect.soft(await problems(page), 'sign-in page with an error').toEqual([])

  // The right password asks for the code, which works once: the next one
  // signs in. Then two-factor authentication goes off again.
  await page.locator('#password').fill('correct horse')
  await page.locator('form button[type=submit]').click()
  await expect(page.locator('#code')).toBeVisible()
  expect.soft(await problems(page), 'sign-in page asking for the code').toEqual([])
  await page.locator('#code').fill(totp(key, 1))
  await page.locator('form button[type=submit]').click()
  await expect(page).not.toHaveURL(/\/login$/)
  await page.goto('./settings')
  await page.getByRole('button', { name: label('kernel.settings.two_factor_turn_off') }).click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'turn off two-factor authentication').toEqual([])
  await dialog.locator('#two-factor-off-password').fill('correct horse')
  await dialog.locator('button[type=submit]').click()
  await expect(page.getByRole('status').filter({ hasText: label('kernel.settings.two_factor_off_done') })).toBeVisible()
  expect.soft(await problems(page), 'two-factor authentication off').toEqual([])

  // The audit log of all that, then what failed, then filtered down to
  // nothing.
  await page.goto('./audit')
  // The table on wide screens, the list on phones: whichever shows.
  await expect(page.getByText(label('audit.op.backup')).filter({ visible: true }).first()).toBeVisible()
  expect.soft(await problems(page), 'audit log').toEqual([])
  await page.getByRole('button', { name: label('audit.result.failed') }).click()
  expect.soft(await problems(page), 'audit log: what failed').toEqual([])
  await page.getByRole('searchbox').fill('nothing.example')
  await expect(page.getByText(label('audit.page.no_match'))).toBeVisible()
  expect.soft(await problems(page), 'audit log with no match').toEqual([])
})

// The dark theme: the main pages again, through axe's checks of color,
// after the walk above has filled the server. The tokens were checked by
// hand for both themes; this keeps the pages to them.
test('the dark theme keeps its contrast', async ({ page }) => {
  test.setTimeout(120_000)
  await page.emulateMedia({ colorScheme: 'dark' })
  const signIn = await page.request.post('api/v1/session', { data: { username: 'admin', password: 'correct horse' } })
  expect(signIn.ok()).toBe(true)
  const pages = ['./', './certificates', './certificates/1', './history', './sites', './sources', './dns', './targets', './notify']
  for (const path of [...pages, './credentials', './modules', './settings', './audit']) {
    await page.goto(path)
    await page.waitForLoadState('networkidle')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    await settle(page)
    const { violations } = await new AxeBuilder({ page }).withRules(['color-contrast', 'link-in-text-block']).analyze()
    expect
      .soft(
        violations.map((v) => `axe ${v.id}: ${v.nodes.slice(0, 3).map((n) => n.target.join(' ')).join(', ')}`),
        `${path} in the dark`,
      )
      .toEqual([])
  }
})
