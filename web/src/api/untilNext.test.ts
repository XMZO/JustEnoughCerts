import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { untilNext } from './certs'

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-19T12:00:00Z'))
})

afterEach(() => {
  vi.useRealTimers()
})

// A page waiting for the next try looks again just after it, but neither
// in a tight loop nor less than twice a minute.
test('untilNext follows the next try within bounds', () => {
  expect(untilNext('2026-10-19T12:00:10Z')).toBe(11_500)
  expect(untilNext('2026-10-19T11:59:00Z')).toBe(2000)
  expect(untilNext('2026-10-19T12:05:00Z')).toBe(30_000)
  expect(untilNext(undefined)).toBe(30_000)
  expect(untilNext('not a time')).toBe(30_000)
})
