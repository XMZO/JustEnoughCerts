import { describe, expect, it } from 'vitest'

import type { Provider, ProviderType } from '../api/providers'
import { applies, initialValues, settings, summary, typeName } from './fields'

// An ACME-like type: a choice, a field for one choice only, a plain and a
// secret setting, and an advanced one.
const type: ProviderType = {
  id: 'acme.account',
  kind: 'source',
  module: 'acme',
  name: 'acme.account.name',
  fields: [
    {
      key: 'ca',
      kind: 'select',
      label: 'acme.field.ca',
      default: 'letsencrypt',
      options: [
        { value: 'letsencrypt', label: 'acme.ca.letsencrypt' },
        { value: 'custom', label: 'acme.ca.custom' },
      ],
    },
    { key: 'directory', kind: 'url', label: 'acme.field.directory', required: true, when: { key: 'ca', value: 'custom' } },
    { key: 'email', kind: 'email', label: 'acme.field.email' },
    { key: 'eab_hmac', kind: 'text', label: 'acme.field.eab_hmac', secret: true },
    { key: 'TTL', kind: 'number', advanced: true },
  ],
}

const instance = (config: Record<string, string>, secretKeys: string[] = []): Provider => ({
  id: 1,
  type: type.id,
  kind: 'source',
  name: 'main',
  config,
  secretKeys,
  createdAt: '2026-10-17T08:00:00Z',
  updatedAt: '2026-10-17T08:00:00Z',
})

describe('applies', () => {
  it('shows a field only for the value it depends on, the default counting', () => {
    const directory = type.fields[1]!
    expect(applies(directory, {}, type.fields)).toBe(false)
    expect(applies(directory, { ca: 'custom' }, type.fields)).toBe(true)
    expect(applies(type.fields[2]!, {}, type.fields)).toBe(true)
  })
})

describe('initialValues', () => {
  it('starts from the defaults, or the instance without its secrets', () => {
    expect(initialValues(type)).toEqual({ ca: 'letsencrypt', directory: '', email: '', eab_hmac: '', TTL: '' })
    expect(initialValues(type, instance({ ca: 'custom', directory: 'https://ca.example/dir' }, ['eab_hmac']))).toEqual({
      ca: 'custom',
      directory: 'https://ca.example/dir',
      email: '',
      eab_hmac: '',
      TTL: '',
    })
  })
})

describe('settings', () => {
  it('splits plain settings from secrets, leaving out empty ones and fields that do not apply', () => {
    const values = { ca: 'letsencrypt', directory: 'https://stale.example', email: ' ', eab_hmac: 'key', TTL: '60' }
    expect(settings(type, values)).toEqual({ config: { ca: 'letsencrypt', TTL: '60' }, secrets: { eab_hmac: 'key' } })
  })

  it('sends no secret left empty, so that an edit keeps the saved one', () => {
    expect(settings(type, { ca: 'letsencrypt', eab_hmac: '' }).secrets).toEqual({})
  })
})

describe('summary', () => {
  it('lists up to two plain settings, choices by their label', () => {
    expect(summary(type, instance({ ca: 'custom', directory: 'https://ca.example/dir', email: 'a@example.com', TTL: '60' }))).toEqual([
      { label: 'acme.field.ca', key: 'ca', value: undefined, option: 'acme.ca.custom' },
      { label: 'acme.field.directory', key: 'directory', value: 'https://ca.example/dir', option: undefined },
    ])
    expect(summary(undefined, instance({ ca: 'custom' }))).toEqual([])
  })
})

describe('typeName', () => {
  it('prefers a brand to a message', () => {
    expect(typeName(type)).toEqual({ key: 'acme.account.name' })
    expect(typeName({ ...type, brand: 'Cloudflare' })).toEqual({ brand: 'Cloudflare' })
    expect(typeName(undefined)).toEqual({})
  })
})
