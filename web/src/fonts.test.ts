import { expect, test } from 'vitest'

import characters from './assets/fonts/characters.json'

// The locale files, as text.
const locales = import.meta.glob<string>('../../i18n/locales/*.json', { query: '?raw', import: 'default', eager: true })

// The ranges of the Chinese font (scripts/fonts.mjs), but for the
// punctuation that the Latin font has too.
const ranges = [
  [0x3000, 0x303f],
  [0x4e00, 0x9fff],
  [0xff00, 0xffef],
]

// The Chinese font is cut down to the characters of the messages. A
// message with one it lacks would show that one in another font: make the
// fonts again.
test('the Chinese font has every Chinese character of the messages', () => {
  const has = new Set(characters.NotoSansSC)
  const missing = new Set<string>()
  for (const text of Object.values(locales)) {
    for (const c of text) {
      const code = c.codePointAt(0) ?? 0
      if (ranges.some(([from = 0, to = 0]) => code >= from && code <= to) && !has.has(c)) missing.add(c)
    }
  }
  expect(Object.keys(locales).length).toBeGreaterThan(1)
  expect([...missing], 'run pnpm -C web fonts').toEqual([])
})
