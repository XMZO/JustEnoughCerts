/**
 * The source, solver, backup source and targets the last certificate was
 * added with, kept in this browser: the add page starts from them, as a run
 * of new certificates mostly shares them.
 */
export interface LastChoice {
  source?: number
  solver?: number
  backup?: number
  targets?: number[]
}

const key = 'jec.add.last'

export function loadLastChoice(): LastChoice {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? '{}') as unknown
    if (!v || typeof v !== 'object') return {}
    const o = v as Record<string, unknown>
    const id = (x: unknown) => (typeof x === 'number' && Number.isInteger(x) ? x : undefined)
    return {
      source: id(o.source),
      solver: id(o.solver),
      backup: id(o.backup),
      targets: Array.isArray(o.targets) ? o.targets.map(id).filter((x): x is number => x !== undefined) : undefined,
    }
  } catch {
    return {}
  }
}

export function saveLastChoice(c: LastChoice) {
  try {
    localStorage.setItem(key, JSON.stringify(c))
  } catch {
    // a browser that keeps nothing starts from the first ones again
  }
}
