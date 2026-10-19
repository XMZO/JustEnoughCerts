// Makes the web fonts of the UI in src/assets/fonts, from the sources of
// Google Fonts at a fixed commit, all under the SIL Open Font License:
// Inter for Latin text, JetBrains Mono for data such as fingerprints, and
// Noto Sans SC (Source Han Sans) cut down to the Chinese characters of the
// locale files. Each keeps its variable weight. characters.json lists the
// Chinese characters the font has, which src/fonts.test.ts holds the
// locale files to: run this again when a message gains a character.
//
//	pnpm fonts
//
// The sources wait in node_modules/.cache/jec-fonts once downloaded.
import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import subsetFont from 'subset-font'

const web = join(dirname(fileURLToPath(import.meta.url)), '..')
const out = join(web, 'src', 'assets', 'fonts')
const cache = join(web, 'node_modules', '.cache', 'jec-fonts')
const locales = join(web, '..', 'i18n', 'locales')

// google/fonts at the commit of 2026-03-25, after the last change to each.
const commit = 'a85815a42757630ce188fdad368c2dfc444d4773'

const range = (from, to) => Array.from({ length: to - from + 1 }, (_, i) => String.fromCodePoint(from + i)).join('')
// Latin: ASCII, Latin-1, Latin Extended-A, general punctuation and arrows.
const latin = range(0x20, 0x7e) + range(0xa0, 0x17f) + range(0x2010, 0x2027) + range(0x2030, 0x203a) + range(0x2190, 0x2193) + range(0x2212, 0x2212)
// The Chinese characters of the locale files, with their punctuation.
const chineseRanges = [[0x2014, 0x2014], [0x2018, 0x2019], [0x201c, 0x201d], [0x2026, 0x2026], [0x3000, 0x303f], [0x4e00, 0x9fff], [0xff00, 0xffef]]
const isChinese = (c) => chineseRanges.some(([from, to]) => c.codePointAt(0) >= from && c.codePointAt(0) <= to)
const chinese = [
  ...new Set(
    readdirSync(locales)
      .filter((f) => f.endsWith('.json'))
      .flatMap((f) => [...readFileSync(join(locales, f), 'utf8')])
      .filter(isChinese),
  ),
]
  .sort()
  .join('')

// Each font, and its license, with their SHA-256.
const fonts = [
  {
    name: 'Inter',
    path: 'ofl/inter/Inter[opsz,wght].ttf',
    sha256: '29160a80ff49ddcab2c97711247e08b1fab27a484a329ce8b813d820dc559031',
    license: '5b9321a4298cfeb6b34354164a1c3afc3db114569984c502b9b35d988fd58c57',
    text: latin,
  },
  {
    name: 'JetBrainsMono',
    path: 'ofl/jetbrainsmono/JetBrainsMono[wght].ttf',
    sha256: '48715a42ec242c21e9f02692891e147d022299a52e48d5e413e1a942193ffeda',
    license: 'b2fe5e8987594e9ffd1d2ca52a2f5d73eb8335243893c5d6254b5ad69269591d',
    text: latin,
  },
  {
    name: 'NotoSansSC',
    path: 'ofl/notosanssc/NotoSansSC[wght].ttf',
    sha256: 'a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da',
    license: '1c05c68c34f9708415aada51f17e1b0092d2cea709bf4a94cd38114f9e73d7d9',
    text: chinese,
  },
]

/** source returns the file at path in google/fonts, checked against its SHA-256. */
async function source(path, sha256) {
  const file = join(cache, path.replaceAll('/', '_'))
  if (!existsSync(file)) {
    const url = `https://raw.githubusercontent.com/google/fonts/${commit}/${path.replace('[', '%5B').replace(']', '%5D')}`
    const res = await fetch(url)
    if (!res.ok) throw new Error(`${url}: ${res.status}`)
    mkdirSync(cache, { recursive: true })
    writeFileSync(file, Buffer.from(await res.arrayBuffer()))
  }
  const data = readFileSync(file)
  const sum = createHash('sha256').update(data).digest('hex')
  if (sum !== sha256) throw new Error(`${path}: SHA-256 ${sum}, want ${sha256}`)
  return data
}

mkdirSync(out, { recursive: true })
for (const f of fonts) {
  // The names, the copyright and the license stay in the font (name IDs 0
  // to 6, 13 and 14), as the license asks.
  const woff2 = await subsetFont(await source(f.path, f.sha256), f.text, {
    targetFormat: 'woff2',
    preserveNameIds: [0, 1, 2, 3, 4, 5, 6, 13, 14],
  })
  writeFileSync(join(out, `${f.name}.woff2`), woff2)
  writeFileSync(join(out, `${f.name}-OFL.txt`), await source(`${dirname(f.path)}/OFL.txt`, f.license))
  console.log(`${f.name}.woff2: ${(woff2.length / 1024).toFixed(0)} KiB`)
}
// Escaped: CJK text belongs in the locale files alone (the i18n checks).
const escaped = (s) => s.replace(/[^\x20-\x7e]/gu, (c) => `\\u${c.codePointAt(0).toString(16).padStart(4, '0')}`)
writeFileSync(join(out, 'characters.json'), `{\n "NotoSansSC": "${escaped(chinese)}"\n}\n`)
console.log(`Noto Sans SC: ${[...chinese].length} characters`)
