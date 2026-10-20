import { beforeEach, describe, expect, it } from 'vitest'

import { loadLastChoice, saveLastChoice } from './lastChoice'

describe('last choice', () => {
  beforeEach(() => localStorage.clear())
  it('keeps what was saved', () => {
    expect(loadLastChoice()).toEqual({})
    saveLastChoice({ source: 3, solver: 5, backup: 4, targets: [1, 2] })
    expect(loadLastChoice()).toEqual({ source: 3, solver: 5, backup: 4, targets: [1, 2] })
  })
  it('drops what it does not understand', () => {
    localStorage.setItem('jec.add.last', '{"source":"x","solver":2.5,"targets":[1,"a",3]}')
    expect(loadLastChoice()).toEqual({ source: undefined, solver: undefined, targets: [1, 3] })
    localStorage.setItem('jec.add.last', 'not json')
    expect(loadLastChoice()).toEqual({})
  })
})
