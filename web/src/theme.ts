/** followSystemTheme keeps data-theme on <html> in step with the OS setting. */
export function followSystemTheme(): void {
  const dark = window.matchMedia('(prefers-color-scheme: dark)')
  const apply = () => {
    document.documentElement.dataset.theme = dark.matches ? 'dark' : 'light'
  }
  apply()
  dark.addEventListener('change', apply)
}
