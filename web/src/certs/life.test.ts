import { describe, expect, it } from 'vitest'

import { barPercent, colonHex, keyText, life, lifeText, mono, windowStart } from './life'

const day = 86_400_000
const now = Date.parse('2026-10-17T08:00:00Z')
const at = (days: number) => new Date(now + days * day).toISOString()

describe('life', () => {
  it('counts whole days and picks the level by days left', () => {
    expect(life(at(-60), at(40), 30, now)).toMatchObject({ days: 40, expired: false, level: 'ok' })
    expect(life(at(-60), at(29.5), 30, now)).toMatchObject({ days: 29, level: 'warn' })
    expect(life(at(-60), at(7), 30, now)).toMatchObject({ days: 7, level: 'danger' })
    expect(life(at(-60), at(10), 5, now)).toMatchObject({ level: 'ok' })
  })

  it('counts the days since expiry', () => {
    expect(life(at(-90), at(-2.5), 30, now)).toMatchObject({ days: 2, expired: true, level: 'danger', fraction: 0 })
  })

  it('measures the share of the validity left', () => {
    expect(life(at(-150), at(50), 30, now).fraction).toBeCloseTo(0.25)
    expect(barPercent(life(at(-150), at(50), 30, now))).toBe(25)
    expect(barPercent(life(at(-999), at(0.5), 30, now))).toBe(2)
    expect(barPercent(life(at(-90), at(-1), 30, now))).toBe(0)
  })
})

describe('lifeText', () => {
  it('words the days left and past', () => {
    expect(lifeText(life(at(-60), at(23.2), 30, now))).toEqual({ key: 'certs.life.left', params: { days: 23 } })
    expect(lifeText(life(at(-60), at(0.2), 30, now))).toEqual({ key: 'certs.life.today', params: {} })
    expect(lifeText(life(at(-60), at(-0.2), 30, now))).toEqual({ key: 'certs.life.expired_today', params: {} })
    expect(lifeText(life(at(-60), at(-3), 30, now))).toEqual({ key: 'certs.life.expired', params: { days: 3 } })
  })
})

describe('mono', () => {
  it('takes two letters, or *. for a wildcard', () => {
    expect(mono('api.example.net')).toBe('ap')
    expect(mono('*.static.example.org')).toBe('*.')
    expect(mono('x.io')).toBe('x.')
  })
})

describe('keyText', () => {
  it('names RSA, ECDSA and Ed25519 keys', () => {
    expect(keyText('rsa-2048')).toEqual({ key: 'certs.key.rsa', params: { bits: '2048' } })
    expect(keyText('ecdsa-p256')).toEqual({ key: 'certs.key.ecdsa', params: { curve: 'P-256' } })
    expect(keyText('ed25519')).toEqual({ key: 'certs.key.ed25519', params: {} })
    expect(keyText('unknown')).toEqual({ key: 'certs.key.unknown', params: {} })
  })
})

describe('colonHex', () => {
  it('writes byte pairs in uppercase', () => {
    expect(colonHex('7c1e930a')).toBe('7C:1E:93:0A')
    expect(colonHex('abc')).toBe('0A:BC')
  })
})

describe('limits', () => {
  it('keeps a short-lived certificate out of its window as it comes', () => {
    const start = Date.parse('2026-10-20T00:00:00Z')
    const iso = (t: number) => new Date(t).toISOString()
    // Six days, a window of 30: due after two, urgent for the last two.
    expect(life(iso(start), iso(start + 6 * day), 30, start + day).level).toBe('ok')
    expect(life(iso(start), iso(start + 6 * day), 30, start + 3 * day).level).toBe('warn')
    expect(life(iso(start), iso(start + 6 * day), 30, start + 5 * day).level).toBe('danger')
    expect(windowStart({ notBefore: iso(start), notAfter: iso(start + 6 * day) }, 30)).toBe(start + 2 * day)
    // Ninety days keep the window of 30.
    expect(windowStart({ notBefore: iso(start), notAfter: iso(start + 90 * day) }, 30)).toBe(start + 60 * day)
  })
})
