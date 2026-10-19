import { ref, watch } from 'vue'

import { crossFade } from './transition'

/** The themes one can choose: the one of the system, or either always. */
export const themes = ['system', 'light', 'dark'] as const
export type Theme = (typeof themes)[number]

// The choice lives in this browser, as the language does.
const storageKey = 'jec.theme'

function stored(): Theme {
  try {
    const v = localStorage.getItem(storageKey)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system'
  }
}

/** theme is the choice of this browser. */
export const theme = ref<Theme>(stored())

/** setTheme makes t the choice of this browser; the page cross-fades to it. */
export function setTheme(t: Theme): void {
  void crossFade(() => {
    theme.value = t
  })
  try {
    if (t === 'system') localStorage.removeItem(storageKey)
    else localStorage.setItem(storageKey, t)
  } catch {
    // A browser that keeps nothing still switches for now.
  }
}

/**
 * The colors of the brand one can choose (docs/design/system/tokens.json,
 * color.accents), green the design's own; and the styles, plain or the
 * blocks of JEI. Both live in this browser too.
 */
export const accents = ['green', 'blue', 'teal', 'purple', 'pink'] as const
export type Accent = (typeof accents)[number]
export const styles = ['plain', 'blocks'] as const
export type Style = (typeof styles)[number]

const accentKey = 'jec.accent'
const styleKey = 'jec.style'

function read<T extends string>(key: string, all: readonly T[], fallback: T): T {
  try {
    const v = localStorage.getItem(key)
    return all.find((a) => a === v) ?? fallback
  } catch {
    return fallback
  }
}

function keep(key: string, value: string, fallback: string): void {
  try {
    if (value === fallback) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch {
    // As for the theme.
  }
}

export const accent = ref<Accent>(read(accentKey, accents, 'green'))
export const style = ref<Style>(read(styleKey, styles, 'plain'))

/** setAccent makes a the color of the brand in this browser. */
export function setAccent(a: Accent): void {
  void crossFade(() => {
    accent.value = a
  })
  keep(accentKey, a, 'green')
}

/** setStyle makes s the style of this browser. */
export function setStyle(s: Style): void {
  void crossFade(() => {
    style.value = s
  })
  keep(styleKey, s, 'plain')
}

/**
 * followTheme keeps data-theme on <html> as chosen, in step with the OS
 * setting while the choice is the system's, and data-accent and
 * data-style, but for the defaults; the color of the browser's bar
 * follows.
 */
export function followTheme(): void {
  const dark = window.matchMedia('(prefers-color-scheme: dark)')
  const root = document.documentElement
  const apply = () => {
    const now = theme.value === 'system' ? (dark.matches ? 'dark' : 'light') : theme.value
    root.dataset.theme = now
    if (accent.value === 'green') delete root.dataset.accent
    else root.dataset.accent = accent.value
    if (style.value === 'plain') delete root.dataset.style
    else root.dataset.style = style.value
    const brand = getComputedStyle(root).getPropertyValue('--brand').trim()
    for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
      // Each tag names the color of one theme; the chosen one's applies
      // everywhere. The light one is the brand's.
      const of = meta.dataset.theme ?? (meta.media.includes('dark') ? 'dark' : 'light')
      meta.dataset.theme = of
      meta.media = of === now ? '' : 'not all'
      if (of === 'light' && now === 'light' && brand) meta.content = brand
    }
  }
  apply()
  dark.addEventListener('change', () => void crossFade(apply))
  watch([theme, accent, style], apply)
}
