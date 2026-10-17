// The pseudo-locale of the i18n gate (PLAN.md 7.4): the English text with
// accented letters, 40% longer and between markers. A test that sees text
// without the markers has found a string that skipped the locale files; one
// that sees text spill out of its box has found a layout too tight for
// languages longer than English.

export const open = '⟦' // ⟦
export const close = '⟧' // ⟧

const plain = 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ'
const accented = 'áƀçďéƒğĥíĵķĺɱñóþʠŕšţúṽŵẋýžÁƁÇĎÉƑĞĤÍĴĶĹṀÑÓÞǪŔŠŢÚṼŴẊÝŽ'
const accents = new Map([...plain].map((c, i) => [c, [...accented][i] ?? c]))

/** pseudoize turns rendered text into its pseudo-locale form. */
export function pseudoize(text: string): string {
  const body = [...text].map((c) => accents.get(c) ?? c).join('')
  // The extra length comes as short words, so that the text still wraps.
  let extra = Math.ceil([...text].length * 0.4)
  const words: string[] = []
  while (extra > 0) {
    const n = Math.min(extra, 5)
    words.push('ẋ'.repeat(n))
    extra -= n + 1
  }
  return `${open}${body}${words.length ? ` ${words.join(' ')}` : ''}${close}`
}

/** pseudoEnabled reports whether this browser asked for the pseudo-locale. */
export function pseudoEnabled(): boolean {
  try {
    return localStorage.getItem('jec.pseudo') === '1'
  } catch {
    return false
  }
}
