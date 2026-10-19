// How the UI shows a certificate's remaining life (docs/design/system:
// LifeBar, CertSlot, StatusBadge): the days left, the color level and the
// words for them.

import type { Certificate, CertificateState } from '../api/certs'
import type { MessageKey, Params } from '../i18n'

const day = 86_400_000

/** Seven days or less left is urgent, as in the backend's expiry notices. */
export const urgentDays = 7

/** The color of a LifeBar or slot. */
export type Level = 'ok' | 'warn' | 'danger'

export interface Life {
  /** Whole days left, or since expiry once expired. */
  days: number
  expired: boolean
  /** The share of the validity left, from 0 to 1. */
  fraction: number
  level: Level
}

/**
 * limits are the renewal window and the urgent span of a version, in
 * milliseconds, as the backend has them: renewBeforeDays and a week, but no
 * more than two thirds and one third of its life.
 */
export function limits(notBefore: string, notAfter: string, renewBeforeDays: number): { window: number; urgent: number } {
  const life = Date.parse(notAfter) - Date.parse(notBefore)
  const window = renewBeforeDays * day
  const urgent = urgentDays * day
  return life > 0 ? { window: Math.min(window, (life * 2) / 3), urgent: Math.min(urgent, life / 3) } : { window, urgent }
}

/** windowStart is when a version enters its renewal window, in milliseconds. */
export const windowStart = (v: { notBefore: string; notAfter: string }, renewBeforeDays: number) =>
  Date.parse(v.notAfter) - limits(v.notBefore, v.notAfter, renewBeforeDays).window

/** life works out where a version stands at now. */
export function life(notBefore: string, notAfter: string, renewBeforeDays: number, now = Date.now()): Life {
  const start = Date.parse(notBefore)
  const end = Date.parse(notAfter)
  const left = end - now
  const expired = left <= 0
  const days = Math.floor(Math.abs(left) / day)
  const fraction = expired ? 0 : Math.min(1, left / Math.max(end - start, 1))
  const { window, urgent } = limits(notBefore, notAfter, renewBeforeDays)
  let level: Level = 'ok'
  if (expired || left <= urgent) level = 'danger'
  else if (left <= window) level = 'warn'
  return { days, expired, fraction, level }
}

/** The life of a certificate's current version, or null without one. */
export function certLife(c: Certificate, now = Date.now()): Life | null {
  return c.current ? life(c.current.notBefore, c.current.notAfter, c.renewBeforeDays, now) : null
}

/** lifeText is the message for the days left: "23 days left". */
export function lifeText(l: Life): { key: MessageKey; params: Params } {
  if (l.expired) {
    return l.days === 0 ? { key: 'certs.life.expired_today', params: {} } : { key: 'certs.life.expired', params: { days: l.days } }
  }
  return l.days === 0 ? { key: 'certs.life.today', params: {} } : { key: 'certs.life.left', params: { days: l.days } }
}

/** barPercent is the fill of a bar: at least 2% while valid, so that its color shows. */
export function barPercent(l: Life): number {
  return l.expired ? 0 : Math.max(2, Math.round(l.fraction * 100))
}

/** The message for each certificate state. */
export const stateText: Record<CertificateState, MessageKey> = {
  ok: 'certs.state.ok',
  expiring: 'certs.state.expiring',
  expired: 'certs.state.expired',
  issuing: 'certs.state.issuing',
  deploying: 'certs.state.deploying',
  failed: 'certs.state.failed',
  deploy_failed: 'certs.state.deploy_failed',
  revoked: 'certs.state.revoked',
}

/** caReasonText names why a CA revoked a version. */
export const caReasonText = (reason?: string) => `certs.ca_revoked.reason.${reason ?? 'unspecified'}` as MessageKey

/** needsAttention tells the certificates that need someone: one being issued or deployed does not, yet. */
export function needsAttention(c: Certificate): boolean {
  return c.state !== 'ok' && c.state !== 'issuing' && c.state !== 'deploying'
}

/** mono is what a slot shows of a domain: two letters of its first label, or "*." for a wildcard. */
export function mono(domain: string): string {
  return domain.startsWith('*.') ? '*.' : domain.slice(0, 2)
}

/** keyText is the message for a key type such as rsa-2048 or ecdsa-p256. */
export function keyText(keyType: string): { key: MessageKey; params: Params } {
  const [kind, size = ''] = keyType.split('-')
  switch (kind) {
    case 'rsa':
      return { key: 'certs.key.rsa', params: { bits: size } }
    case 'ecdsa':
      return { key: 'certs.key.ecdsa', params: { curve: size.toUpperCase().replace(/^P(\d)/, 'P-$1') } }
    case 'ed25519':
      return { key: 'certs.key.ed25519', params: {} }
  }
  return { key: 'certs.key.unknown', params: {} }
}

/** colonHex writes lowercase hex as uppercase byte pairs: 7c1e93 → 7C:1E:93. */
export function colonHex(hex: string): string {
  const even = hex.length % 2 ? `0${hex}` : hex
  return (even.match(/../g) ?? []).join(':').toUpperCase()
}
