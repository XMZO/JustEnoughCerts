import { ref, watch } from 'vue'

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

/** setTheme makes t the choice of this browser. */
export function setTheme(t: Theme): void {
  theme.value = t
  try {
    if (t === 'system') localStorage.removeItem(storageKey)
    else localStorage.setItem(storageKey, t)
  } catch {
    // A browser that keeps nothing still switches for now.
  }
}

/**
 * followTheme keeps data-theme on <html> as chosen, in step with the OS
 * setting while the choice is the system's; the color of the browser's bar
 * follows.
 */
export function followTheme(): void {
  const dark = window.matchMedia('(prefers-color-scheme: dark)')
  const apply = () => {
    const now = theme.value === 'system' ? (dark.matches ? 'dark' : 'light') : theme.value
    document.documentElement.dataset.theme = now
    for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
      // Each tag names the color of one theme; the chosen one's applies everywhere.
      const of = meta.dataset.theme ?? (meta.media.includes('dark') ? 'dark' : 'light')
      meta.dataset.theme = of
      meta.media = of === now ? '' : 'not all'
    }
  }
  apply()
  dark.addEventListener('change', apply)
  watch(theme, apply)
}
