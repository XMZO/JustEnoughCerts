import { ref } from 'vue'

// The time zone the panel shows times in (PLAN.md 11): the one chosen in
// the settings, or the server's, as the manifest tells it. Until the
// manifest comes, and for a zone the browser does not know, times are in
// the browser's.

/** zone is the IANA name of the panel's time zone; undefined for the browser's. */
export const zone = ref<string>()

const known = new Map<string, boolean>()

/** setZone makes the zone named the one times are shown in, if the browser knows it. */
export function setZone(name: string | undefined) {
  zone.value = name && supported(name) ? name : undefined
}

function supported(name: string): boolean {
  let ok = known.get(name)
  if (ok === undefined) {
    try {
      new Intl.DateTimeFormat('en-US', { timeZone: name })
      ok = true
    } catch {
      ok = false
    }
    known.set(name, ok)
  }
  return ok
}

/** zones are the zones to choose from: those the browser knows, with UTC and any given. */
export function zones(...more: string[]): string[] {
  const list = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : []
  return [...new Set([...list, 'UTC', ...more.filter(Boolean)])].sort()
}

// The parts of a time in the zone, as numbers written with two digits.
const formats = new Map<string, Intl.DateTimeFormat>()
function parts(at: string | number | Date) {
  const key = zone.value ?? ''
  let f = formats.get(key)
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone: zone.value,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23',
    })
    formats.set(key, f)
  }
  return Object.fromEntries(f.formatToParts(new Date(at)).map((p) => [p.type, p.value])) as Record<Intl.DateTimeFormatPartTypes, string>
}

/** dayKey names the day a time falls on in the zone, as 2026-10-19, to group by day. */
export function dayKey(at: string | number | Date): string {
  const p = parts(at)
  return `${p.year}-${p.month}-${p.day}`
}

/** stamp writes a time in the zone as 2026-10-19 14:20, or 14:20:05 with seconds, for files people export. */
export function stamp(at: string | number | Date, seconds = false): string {
  const p = parts(at)
  return `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}${seconds ? `:${p.second}` : ''}`
}
