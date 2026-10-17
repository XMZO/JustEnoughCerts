// The web UI and the backend must render a message the same way. These
// cases mirror i18n/message_test.go.
import IntlMessageFormat from 'intl-messageformat'
import { describe, expect, it } from 'vitest'

import { preferredLocale } from '.'

const format = (locale: string, src: string, args: Record<string, string | number> = {}) =>
  String(new IntlMessageFormat(src, locale, undefined, { ignoreTag: true }).format(args))

const days = '{n, plural, =0 {no days} one {# day} other {# days}}'

describe('message format', () => {
  it.each([
    ['en-US', 'plain text', {}, 'plain text'],
    ['en-US', 'Renew {domain} now', { domain: 'example.com' }, 'Renew example.com now'],
    ['en-US', 'a {n} b', { n: 12345 }, 'a 12345 b'],
    ['en-US', "don't", {}, "don't"],
    ['en-US', "It''s", {}, "It's"],
    ['en-US', "'{literal}'", {}, '{literal}'],
    ['en-US', "'{a''b}' c", {}, "{a'b} c"],
    ['en-US', "use '<b>' tags", {}, 'use <b> tags'],
    ['en-US', "# and '# outside plural", {}, "# and '# outside plural"],
    ['en-US', "{n, plural, other {'#' is #}}", { n: 5 }, '# is 5'],
    ['en-US', days, { n: 0 }, 'no days'],
    ['en-US', days, { n: 1 }, '1 day'],
    ['en-US', days, { n: 1234567 }, '1,234,567 days'],
    ['zh-CN', '{n, plural, other {# items}}', { n: 1234 }, '1,234 items'],
    ['ru', '{n, plural, one {one} few {few} many {many} other {other}}', { n: 22 }, 'few'],
    ['en-US', '{kind, select, dns {DNS} http {HTTP} other {check}}', { kind: 'email' }, 'check'],
    [
      'en-US',
      '{n, plural, one {{kind, select, dns {one DNS record #} other {one check}}} other {# checks}}',
      { n: 1, kind: 'dns' },
      'one DNS record #',
    ],
  ])('%s %s', (locale, src, args, want) => {
    expect(format(locale, src, args)).toBe(want)
  })
})

describe('preferredLocale', () => {
  it.each([
    [['zh-CN', 'en'], 'zh-CN'],
    [['zh'], 'zh-CN'],
    [['zh-TW'], 'zh-CN'],
    [['en-GB'], 'en-US'],
    [['de-DE', 'fr'], 'en-US'],
    [[], 'en-US'],
  ])('%j', (prefs, want) => {
    expect(preferredLocale(prefs)).toBe(want)
  })
})
