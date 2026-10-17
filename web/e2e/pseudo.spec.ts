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

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('jec.pseudo', '1'))
})

/** problems lists text without markers and text that does not fit. */
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
    if (width > window.innerWidth + 1) found.push(`the page scrolls sideways: ${width}px wide in a ${window.innerWidth}px window`)
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
})
