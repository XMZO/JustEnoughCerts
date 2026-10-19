import { useT } from '.'
import { zone } from '../zone'

/**
 * useIntl formats dates and lists in the current language, the dates in
 * the panel's time zone (see zone.ts). Text formatted
 * here is not from the locale files, so where it stands alone the template
 * puts it in a <time> or <data> element, which the pseudo-locale test skips.
 */
export function useIntl() {
  const { locale } = useT()
  return {
    /** date: Oct 21, 2026 in English. */
    date: (at: string | number | Date) =>
      new Intl.DateTimeFormat(locale.value, { timeZone: zone.value, year: 'numeric', month: 'short', day: 'numeric' }).format(new Date(at)),
    /** shortDate: Oct 21 in English. */
    shortDate: (at: string | number | Date) =>
      new Intl.DateTimeFormat(locale.value, { timeZone: zone.value, month: 'short', day: 'numeric' }).format(new Date(at)),
    /** time: 14:20:02, the time of day to the second. */
    time: (at: string | number | Date) =>
      new Intl.DateTimeFormat(locale.value, { timeZone: zone.value, hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(at)),
    /** clock: 14:20, the time of day to the minute. */
    clock: (at: string | number | Date) => new Intl.DateTimeFormat(locale.value, { timeZone: zone.value, hour: '2-digit', minute: '2-digit' }).format(new Date(at)),
    /** dateTime: Oct 21, 14:20 in English. */
    dateTime: (at: string | number | Date) =>
      new Intl.DateTimeFormat(locale.value, { timeZone: zone.value, month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(at)),
    /** bytes: 1.5 GB, in the largest unit (of 1024) that keeps the number at least 1. */
    bytes: (n: number) => {
      const units = ['byte', 'kilobyte', 'megabyte', 'gigabyte', 'terabyte'] as const
      let i = 0
      while (n >= 1024 && i < units.length - 1) {
        n /= 1024
        i++
      }
      return new Intl.NumberFormat(locale.value, { style: 'unit', unit: units[i], unitDisplay: 'short', maximumFractionDigits: n < 10 ? 1 : 0 }).format(n)
    },
    /** list: a, b, and c in English. */
    list: (items: string[]) => new Intl.ListFormat(locale.value, { type: 'conjunction' }).format(items),
  }
}
