import IntlMessageFormat from 'intl-messageformat'
import { createI18n, useI18n, type MessageCompiler, type MessageContext } from 'vue-i18n'

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

/** Switches the UI language, loading its messages first. */
export async function setLocale(tag: Locale, remember = true): Promise<void> {
  await load(baseLocale) // the fallback for missing translations
  await load(tag)
  i18n.global.locale.value = tag
  const meta = locales.find((l) => l.tag === tag)
  document.documentElement.lang = tag
  document.documentElement.dir = meta?.dir ?? 'ltr'
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
