import { afterEach, describe, expect, it } from 'vitest'

import { dayKey, setZone, stamp, zone, zones } from './zone'

afterEach(() => setZone(undefined))

describe('zone', () => {
  it('takes the zones the browser knows', () => {
    setZone('Asia/Shanghai')
    expect(zone.value).toBe('Asia/Shanghai')
    setZone('Mars/Olympus_Mons')
    expect(zone.value).toBeUndefined()
    setZone('')
    expect(zone.value).toBeUndefined()
  })

  it('tells the day and the time in the zone', () => {
    const at = '2026-10-19T17:05:09Z'
    setZone('UTC')
    expect(dayKey(at)).toBe('2026-10-19')
    expect(stamp(at)).toBe('2026-10-19 17:05')
    setZone('Asia/Shanghai')
    expect(dayKey(at)).toBe('2026-10-20')
    expect(stamp(at, true)).toBe('2026-10-20 01:05:09')
    setZone('America/New_York')
    expect(stamp('2026-10-19T04:00:00Z')).toBe('2026-10-19 00:00')
  })

  it('lists UTC and the zone chosen among the zones', () => {
    const list = zones('Etc/GMT-14')
    expect(list).toContain('UTC')
    expect(list).toContain('Etc/GMT-14')
    expect(list).toEqual([...list].sort())
  })
})
