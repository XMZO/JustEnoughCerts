import { describe, expect, it } from 'vitest'

import { covers, matches, searchTerm } from './search'

describe('search', () => {
  it('reads a URL by its host', () => {
    expect(searchTerm('  https://API.Shop.com:8443/path?q=1 ')).toBe('api.shop.com')
    expect(searchTerm('Shop')).toBe('shop')
  })
  it('finds the wildcard that covers a host', () => {
    expect(covers('*.shop.com', 'api.shop.com')).toBe(true)
    expect(covers('*.shop.com', 'a.b.shop.com')).toBe(false)
    expect(covers('*.shop.com', 'shop.com')).toBe(false)
    expect(covers('*.shop.com', '.shop.com')).toBe(false)
    expect(covers('shop.com', 'api.shop.com')).toBe(false)
  })
  it('matches domains, wildcards and notes', () => {
    const c = { domains: ['shop.com', '*.shop.com'], note: 'For the Shop team' }
    expect(matches(c, '')).toBe(true)
    expect(matches(c, 'shop')).toBe(true)
    expect(matches(c, 'api.shop.com')).toBe(true)
    expect(matches(c, 'team')).toBe(true)
    expect(matches(c, 'other.com')).toBe(false)
  })
})
