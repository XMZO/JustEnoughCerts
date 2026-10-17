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
    /** time: 14:20:02, the time of day to the second. */
    time: (at: string | number | Date) =>
      new Intl.DateTimeFormat(locale.value, { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(at)),
    /** dateTime: Oct 21, 14:20 in English. */
    dateTime: (at: string | number | Date) =>
      new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(at)),
    /** list: a, b, and c in English. */
    list: (items: string[]) => new Intl.ListFormat(locale.value, { type: 'conjunction' }).format(items),
  }
}
