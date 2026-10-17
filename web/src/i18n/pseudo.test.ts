import { describe, expect, it } from 'vitest'

import { close, open, pseudoize } from './pseudo'

describe('pseudoize', () => {
  it('marks, accents and lengthens', () => {
    const got = pseudoize('Sign in')
    expect(got.startsWith(`${open}Šíğñ íñ`)).toBe(true)
    expect(got.endsWith(close)).toBe(true)
    expect([...got].length - 2).toBeGreaterThanOrEqual(Math.ceil('Sign in'.length * 1.4))
  })

  it('keeps digits, spaces and punctuation', () => {
    expect(pseudoize('5 min.').slice(1, 7)).toBe('5 ɱíñ.')
  })

  it('pads with short words that can wrap', () => {
    const padding = pseudoize('x'.repeat(50)).slice(51, -1)
    for (const word of padding.trim().split(' ')) expect([...word].length).toBeLessThanOrEqual(5)
  })

  it('leaves empty text marked but empty', () => {
    expect(pseudoize('')).toBe(`${open}${close}`)
  })
})
