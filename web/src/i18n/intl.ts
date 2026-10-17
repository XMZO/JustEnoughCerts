import { useT } from '.'

/**
 * useIntl formats dates and lists in the current language. Text formatted
 * here is not from the locale files, so where it stands alone the template
 * puts it in a <time> or <data> element, which the pseudo-locale test skips.
 */
export function useIntl() {
  const { locale } = useT()
  return {
    /** date: Oct 21, 2026 in English. */
    date: (at: string | number | Date) =>
      new Intl.DateTimeFormat(locale.value, { year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(at)),
    /** shortDate: Oct 21 in English. */
    shortDate: (at: string | number | Date) =>
      new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric' }).format(new Date(at)),
    /** list: a, b, and c in English. */
    list: (items: string[]) => new Intl.ListFormat(locale.value, { type: 'conjunction' }).format(items),
  }
}
