// Messages that come with data, as plugins bring their texts.
import { describe, expect, it } from 'vitest'

import { addMessages, i18n, setLocale } from '.'

// The keys are not in the locale files: the lint of keys does not see them.
const t = (key: string) => i18n.global.t(key)
const name = 'plugins.texts.x.cdn.name'
const help = 'plugins.texts.x.cdn.help'

describe('addMessages', () => {
  it('adds texts before and after their locale loads', async () => {
    addMessages({ 'en-US': { 'plugins.texts.x.cdn.name': 'X CDN' } })
    await setLocale('en-US', false)
    expect(t(name)).toBe('X CDN')
    // As the plugins module escapes them: braces and apostrophes as they are.
    addMessages({ 'en-US': { 'plugins.texts.x.cdn.help': "Use '{a}' and it''s fine" } })
    expect(t(help)).toBe("Use {a} and it's fine")
    // Texts of a locale not loaded yet wait for it, and the locale file
    // does not replace them.
    addMessages({ 'zh-CN': { 'plugins.texts.x.cdn.name': 'X CDN (zh)' } })
    await setLocale('zh-CN', false)
    expect(t(name)).toBe('X CDN (zh)')
    expect(t(help)).toBe("Use {a} and it's fine")
    await setLocale('en-US', false)
  })
})
