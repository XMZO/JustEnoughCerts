import { describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

describe('theme', () => {
  it("follows the system's, or the choice of this browser, which it keeps", async () => {
    let dark = false
    const changes: (() => void)[] = []
    vi.stubGlobal('matchMedia', (media: string) => ({
      get matches() {
        return dark
      },
      media,
      addEventListener: (_: string, f: () => void) => changes.push(f),
    }))
    const { followTheme, setTheme } = await import('./theme')
    const now = () => document.documentElement.dataset.theme

    followTheme()
    expect(now()).toBe('light')
    dark = true
    for (const f of changes) f()
    expect(now()).toBe('dark')

    setTheme('light')
    await nextTick()
    expect(now()).toBe('light')
    expect(localStorage.getItem('jec.theme')).toBe('light')

    setTheme('system')
    await nextTick()
    expect(now()).toBe('dark')
    expect(localStorage.getItem('jec.theme')).toBeNull()
    vi.unstubAllGlobals()
  })
})
