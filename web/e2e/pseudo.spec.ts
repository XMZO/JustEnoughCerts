import { createHmac } from 'node:crypto'
import { readFileSync } from 'node:fs'

import { expect, test, type Page } from '@playwright/test'

import { pseudoize } from '../src/i18n/pseudo'
import { selfSigned } from './certs'

// Every page, in the pseudo-locale (src/i18n/pseudo.ts): all text a user
// sees must carry the markers, and none of it may spill out of its box.

const english = JSON.parse(readFileSync(new URL('../../i18n/locales/en-US.json', import.meta.url), 'utf8')) as Record<string, unknown>

/** label is how a plain message reads in the pseudo-locale, to find a button by its name. */
function label(key: string): string {
  const text = key.split('.').reduce<unknown>((node, part) => (node as Record<string, unknown>)[part], english)
  return pseudoize(String(text))
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

async function problems(page: Page): Promise<string[]> {
  await page.waitForLoadState('networkidle')
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
  const found = await problems(page)
  expect(found).toEqual([
    'text without markers in <p class="">: hard-coded',
    `text without markers in <p class="">: ⟦fine⟧ and raw`,
    'title without markers on <p class="">: raw tooltip',
    expect.stringMatching(/^overflowing text in <div class="">: ⟦w+/),
    expect.stringMatching(/^clipped text in <div class="">: ⟦c+/),
  ])
})

// Soft checks: one run reports the problems of every page, not just the first.
test('every page is translated and fits', async ({ page }, info) => {
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
  await page.goto('./certificates/1')
  expect.soft(await problems(page), 'a certificate').toEqual([])

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
  await dialog.getByRole('option', { name: label('acme.account.name') }).click()
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
  await dialog.getByRole('option', { name: 'HTTP request' }).click()
  await expect(dialog.getByLabel('HTTPREQ_ENDPOINT')).toBeVisible()
  expect.soft(await problems(page), 'add a DNS provider: its form').toEqual([])
  await dialog.getByLabel('HTTPREQ_ENDPOINT').fill('http://127.0.0.1:9')
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  expect.soft(await problems(page), 'domain validation with one').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.solver.add') }).filter({ visible: true }).first().click()
  await dialog.getByRole('searchbox').fill('wellknown')
  await dialog.getByRole('option', { name: label('wellknown.answer.name') }).click()
  await expect(dialog.getByLabel(label('wellknown.field.domain'))).toBeVisible()
  expect.soft(await problems(page), 'add a validation answered by JEC').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  await page.getByRole('button', { name: label('kernel.providers.solver.add') }).filter({ visible: true }).first().click()
  await dialog.getByRole('searchbox').fill('esa.edge')
  await dialog.getByRole('option', { name: label('esa.edge.name') }).click()
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
  await dialog.getByRole('option', { name: label('esa.site.name') }).click()
  await expect(dialog.getByLabel(label('esa.field.site'))).toBeVisible()
  expect.soft(await problems(page), 'add a deploy target: ESA').toEqual([])
  for (const [type, field] of [
    ['ssh.server.name', 'ssh.field.host_key'],
    ['webhook.deploy.name', 'webhook.field.secret'],
  ] as const) {
    if (info.project.name === 'phone') await dialog.getByRole('button', { name: label('kernel.providers.choose_other') }).click()
    await dialog.getByRole('option', { name: label(type) }).click()
    await expect(dialog.getByLabel(label(field))).toBeVisible()
    expect.soft(await problems(page), `add a deploy target: ${type}`).toEqual([])
  }
  // A plugin's type, with the texts it brings, where shell scripts run.
  if (process.platform !== 'win32') {
    if (info.project.name === 'phone') await dialog.getByRole('button', { name: label('kernel.providers.choose_other') }).click()
    await dialog.getByRole('option', { name: pseudoize('E2E plugin target') }).click()
    await expect(dialog.getByLabel(pseudoize('Endpoint'))).toBeVisible()
    await expect(dialog.getByText(pseudoize('Where it {would} deploy'))).toBeVisible()
    expect.soft(await problems(page), 'add a deploy target: a plugin').toEqual([])
  }
  if (info.project.name === 'phone') await dialog.getByRole('button', { name: label('kernel.providers.choose_other') }).click()
  await dialog.getByRole('option', { name: label('files.directory.name') }).click()
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e files')
  await dialog.getByLabel(label('files.field.directory')).fill(`${process.env.JEC_E2E_FILES}/${info.project.name}`)
  expect.soft(await problems(page), 'add a deploy target: a directory').toEqual([])
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  expect.soft(await problems(page), 'deploy targets with one').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.test') }).click()
  await expect(page.getByRole('status')).toBeVisible()
  expect.soft(await problems(page), 'deploy targets: a test that passed').toEqual([])

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
  await dialog.getByRole('option', { name: label('files.watch.name') }).click()
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
  await dialog.getByRole('option', { name: label('files.directory.name') }).click()
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e relative')
  await dialog.getByLabel(label('files.field.directory')).fill('relative/certs')
  await dialog.locator('button[type=submit]').click()
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

  // Notifications: Telegram's form, then a webhook to this machine, whose
  // test the guard against private addresses refuses.
  await page.goto('./notify')
  expect.soft(await problems(page), 'notifications').toEqual([])
  await page.getByRole('button', { name: label('kernel.providers.notifier.add') }).filter({ visible: true }).first().click()
  await expect(dialog).toBeVisible()
  expect.soft(await problems(page), 'add a channel').toEqual([])
  await dialog.getByRole('option', { name: label('telegram.bot.name') }).click()
  await expect(dialog.getByLabel(label('telegram.field.chat'))).toBeVisible()
  expect.soft(await problems(page), 'add a channel: Telegram').toEqual([])
  if (info.project.name === 'phone') await dialog.getByRole('button', { name: label('kernel.providers.choose_other') }).click()
  await dialog.getByRole('option', { name: label('webhook.notify.name') }).click()
  await dialog.getByLabel(label('kernel.providers.field.name')).fill('e2e hook')
  await dialog.getByLabel(label('webhook.field.url')).fill('http://127.0.0.1:9/hook')
  await dialog.locator('button[type=submit]').click()
  await expect(page.locator('dialog[open]')).toHaveCount(0)
  expect.soft(await problems(page), 'notifications with one').toEqual([])
  await page.getByRole('button', { name: label('notify.action.test') }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  expect.soft(await problems(page), 'a test that failed').toEqual([])

  // A certificate from the source: the form, its errors, the issuance.
  await page.goto('./certificates/new')
  await expect(page.locator('#domain-input')).toBeVisible()
  expect.soft(await problems(page), 'add a certificate from a source').toEqual([])
  await page.locator('button[form=add-form]').click()
  await expect(page.locator('#domains-help')).toHaveText(label('kernel.error.invalid_field'))
  expect.soft(await problems(page), 'add a certificate from a source with an error').toEqual([])
  await page.locator('#domain-input').fill('issued.example.com')
  await page.locator('#domain-input').press('Enter')
  await page.locator('#domain-input').fill('*.issued.example.com')
  await page.locator('#domain-input').press('Enter')
  await page.locator('button[form=add-form]').click()
  await expect(page).toHaveURL(/\/certificates\/\d+\/issuances\/\d+$/)
  await expect(page.getByText(label('certs.issuance.next_try')).first()).toBeVisible() // the first attempt failed
  expect.soft(await problems(page), 'an issuance that failed').toEqual([])
  await page.getByRole('checkbox', { name: label('certs.issuance.show_detail') }).check()
  expect.soft(await problems(page), 'an issuance with raw details').toEqual([])
  await page.getByRole('link', { name: label('certs.action.details') }).filter({ visible: true }).first().click()
  await expect(page).toHaveURL(/\/certificates\/\d+$/)
  expect.soft(await problems(page), 'a certificate whose issuance failed').toEqual([])
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
  await dialog.getByRole('option', { name: label('namecheap.ssl_manager.name') }).click()
  await expect(dialog.getByLabel(label('namecheap.field.token'))).toBeVisible()
  expect.soft(await problems(page), 'add a source: Namecheap').toEqual([])
  await dialog.getByRole('button', { name: label('kernel.action.cancel') }).click()

  // The settings: a default saved at once, then one the server refuses.
  await page.goto('./settings')
  const days = page.locator('#renewBeforeDays')
  await expect(days).toBeVisible()
  expect.soft(await problems(page), 'settings').toEqual([])
  await days.fill('45')
  await days.blur()
  await expect(page.getByRole('status').filter({ hasText: label('kernel.settings.saved') })).toBeVisible()
  expect.soft(await problems(page), 'settings saved').toEqual([])
  await days.fill('0')
  await days.blur()
  await expect(days).toHaveAttribute('aria-invalid', 'true')
  expect.soft(await problems(page), 'settings refused').toEqual([])
  await days.fill('30')
  await days.blur()
  await expect(days).not.toHaveAttribute('aria-invalid')
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

  await page.goto('./more')
  expect.soft(await problems(page), 'more').toEqual([])
  await page.getByRole('button', { name: label('kernel.shell.sign_out') }).filter({ visible: true }).first().click()
  await expect(page).toHaveURL(/\/login$/)
  expect.soft(await problems(page), 'sign-in page').toEqual([])

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
