import IntlMessageFormat from 'intl-messageformat'
import { createI18n, useI18n, type MessageCompiler, type MessageContext } from 'vue-i18n'

import { crossFade } from '../transition'
import { baseLocale, locales, type Locale, type MessageKey } from './generated'
import { pseudoEnabled, pseudoize } from './pseudo'

export { locales, type Locale, type MessageKey }

// The pseudo-locale renders English through pseudoize; the e2e tests turn
// it on with localStorage "jec.pseudo" = "1".
const pseudo = pseudoEnabled()

type Messages = Record<string, unknown>

// The locale files are shared with the backend; each is loaded when first
// needed, so only the languages in use are downloaded.
const files = import.meta.glob<{ default: Messages }>('../../../i18n/locales/*.json')

// Messages use the ICU MessageFormat subset that the backend parses too
// (i18n/message.go); FormatJS renders them the same way. Tags stay text.
const messageCompiler: MessageCompiler = (message, ctx) => {
  if (typeof message !== 'string') {
    ctx.onError?.(Object.assign(new Error('message is not a string'), { code: 0 }))
    return () => ctx.key
  }
  const format = new IntlMessageFormat(message, ctx.locale, undefined, { ignoreTag: true })
  return (mctx: MessageContext) => {
    const text = String(format.format(mctx.values as Record<string, string | number>))
    return pseudo ? pseudoize(text) : text
  }
}

export const i18n = createI18n({
  legacy: false,
  locale: baseLocale,
  fallbackLocale: baseLocale,
  messages: {},
  messageCompiler,
  missingWarn: false,
  fallbackWarn: false,
})

const storageKey = 'jec.locale'
const loaded = new Set<string>()
// Messages that come with data instead of the locale files, such as the
// texts plugins bring, by locale and key: added to a locale's messages now
// if they are loaded, or once they are.
const extra = new Map<string, Record<string, string>>()

/** nest turns flat keys, such as a.b.c, into the nested messages vue-i18n keeps. */
function nest(flat: Record<string, string>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [key, text] of Object.entries(flat)) {
    const parts = key.split('.')
    const last = parts.pop()!
    let node = out
    for (const part of parts) {
      const next = node[part]
      node = (typeof next === 'object' && next !== null ? next : (node[part] = {})) as Record<string, unknown>
    }
    node[last] = text
  }
  return out
}

/**
 * addMessages adds messages that are not in the locale files, by locale
 * tag and key, such as the texts a plugin brings for its types.
 */
export function addMessages(texts: Record<string, Record<string, string>> | undefined): void {
  for (const [tag, messages] of Object.entries(texts ?? {})) {
    extra.set(tag, { ...extra.get(tag), ...messages })
    if (loaded.has(tag)) i18n.global.mergeLocaleMessage(tag, nest(messages) as never)
  }
}

function isLocale(tag: string): tag is Locale {
  return locales.some((l) => l.tag === tag)
}

async function load(tag: Locale): Promise<void> {
  if (loaded.has(tag)) return
  const file = files[`../../../i18n/locales/${tag}.json`]
  if (!file) throw new Error(`no locale file for ${tag}`)
  const messages = { ...(await file()).default }
  delete messages._meta
  i18n.global.setLocaleMessage(tag, messages as never)
  const more = extra.get(tag)
  if (more) i18n.global.mergeLocaleMessage(tag, nest(more) as never)
  loaded.add(tag)
}

/** The locale that best fits the browser's preferences. */
export function preferredLocale(preferences: readonly string[] = navigator.languages): Locale {
  for (const pref of preferences) {
    if (isLocale(pref)) return pref
    const lang = pref.split('-')[0]
    const match = locales.find((l) => l.tag.split('-')[0] === lang)
    if (match) return match.tag
  }
  return baseLocale
}

/**
 * Switches the UI language, loading its messages first. A language someone
 * chose, which is remembered, cross-fades in; the page stays as it is.
 */
export async function setLocale(tag: Locale, remember = true): Promise<void> {
  await load(baseLocale) // the fallback for missing translations
  await load(tag)
  const apply = () => {
    i18n.global.locale.value = tag
    const meta = locales.find((l) => l.tag === tag)
    document.documentElement.lang = tag
    document.documentElement.dir = meta?.dir ?? 'ltr'
  }
  if (remember) await crossFade(apply)
  else apply()
  if (remember) {
    try {
      localStorage.setItem(storageKey, tag)
    } catch {
      // storage may be unavailable; the choice then lasts for this visit
    }
  }
}

/** The locale chosen earlier on this device, or the browser's preference. */
export function initialLocale(): Locale {
  if (pseudo) return baseLocale
  try {
    const saved = localStorage.getItem(storageKey)
    if (saved && isLocale(saved)) return saved
  } catch {
    // fall through
  }
  return preferredLocale()
}

export type Params = Record<string, string | number>

/** useT is useI18n with typed message keys. */
export function useT() {
  const { t, locale } = useI18n({ useScope: 'global' })
  return {
    t: (key: MessageKey, params: Params = {}) => t(key, params),
    locale,
  }
}
