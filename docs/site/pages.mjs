// Puts the pages of the documentation site together in src/ from docs/ of
// the repository: English at the root, Chinese under zh-CN/. The home pages
// are the site's own, home.md and home.zh-CN.md here; every other page is
// docs/<section>/<page>.md, with its Chinese twin beside it as
// <page>.zh-CN.md. Links between those pages point at the pages of the
// site, named after their titles where the text was a file name, and links
// to the rest of the repository at GitHub. The line that links a page in
// the other language goes, as the site has a menu of languages, and
// GitHub's alerts become the site's boxes, with titles in the page's
// language.
//
//	node pages.mjs
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join, posix } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const repo = join(here, '..', '..')
const src = join(here, 'src')
const github = 'https://github.com/XMZO/JustEnoughCerts'

// The sections of the site, in the order of the sidebar, with their pages.
export const sections = [
  { dir: 'guide', pages: ['introduction', 'getting-started', 'installation', 'concepts', 'faq'] },
  { dir: 'usage', pages: ['certificates', 'sources', 'validation', 'targets', 'renewal', 'notifications'] },
  { dir: 'operations', pages: ['configuration', 'reverse-proxy', 'backup', 'security', 'config-file', 'releases'] },
  { dir: 'reference', pages: ['providers', 'api'] },
  { dir: 'development', pages: ['contributing', 'plugins', 'translating', 'testing'] },
]

/** source names the file of the repository a page of the section dir comes from. */
export function source(dir, name, lang) {
  return `docs/${dir}/${name}${lang === 'zh-CN' ? '.zh-CN' : ''}.md`
}

// Where each page comes from in the repository, and where it goes. The
// home pages are copied as they are.
export const pages = [
  { from: 'docs/site/home.md', to: 'index.md', home: true },
  { from: 'docs/site/home.zh-CN.md', to: 'zh-CN/index.md', home: true },
  ...sections.flatMap((s) =>
    s.pages.flatMap((name) => [
      { from: source(s.dir, name, 'en'), to: `${s.dir}/${name}.md` },
      { from: source(s.dir, name, 'zh-CN'), to: `zh-CN/${s.dir}/${name}.md` },
    ]),
  ),
]
const pageOf = new Map(pages.map((p) => [p.from, p.to]))

// The titles of the pages, by where they come from: their first heading.
export const titleOf = new Map(
  pages.map((p) => {
    const file = join(repo, p.from)
    const text = existsSync(file) ? readFileSync(file, 'utf8') : ''
    return [p.from, (text.match(/^# (.+)$/m) ?? [])[1] ?? p.to]
  }),
)

// The kinds of GitHub's alerts, as the site's boxes with titles.
const alerts = {
  en: { NOTE: ['info', 'Note'], TIP: ['tip', 'Tip'], IMPORTANT: ['info', 'Important'], WARNING: ['warning', 'Warning'], CAUTION: ['danger', 'Caution'] },
  'zh-CN': { NOTE: ['info', '说明'], TIP: ['tip', '提示'], IMPORTANT: ['info', '重要'], WARNING: ['warning', '注意'], CAUTION: ['danger', '警告'] },
}

/** sourceOf names the file of the repository the page of the site at path, as in src/, comes from. */
export function sourceOf(path) {
  return pages.find((p) => p.to === path)?.from
}

/** unlisted returns the pages of the sections' directories missing from sections, and the listed ones missing from the directories. */
export function unlisted() {
  const listed = new Set(pages.map((p) => p.from))
  const found = sections.flatMap((s) =>
    readdirSync(join(repo, 'docs', s.dir))
      .filter((f) => f.endsWith('.md'))
      .map((f) => `docs/${s.dir}/${f}`),
  )
  return [...found.filter((f) => !listed.has(f)), ...[...listed].filter((f) => !existsSync(join(repo, f)))]
}

/** link returns where a link of the page from, to target, goes on the site. */
export function link(from, to, target) {
  if (/^[a-z][a-z0-9+.-]*:/i.test(target) || target.startsWith('#') || target.startsWith('/')) return target
  const [path, hash] = target.split('#')
  const at = posix.normalize(posix.join(posix.dirname(from), path)).replace(/\/$/, '')
  const anchor = hash ? `#${hash}` : ''
  const page = pageOf.get(at)
  if (page) {
    const rel = posix.relative(posix.dirname(to), page)
    return (rel.startsWith('.') ? rel : `./${rel}`) + anchor
  }
  const dir = existsSync(join(repo, at)) && statSync(join(repo, at)).isDirectory()
  return `${github}/${dir ? 'tree' : 'blob'}/main/${at}${anchor}`
}

/** linkText names a link to a page after the page, where it named the file. */
function linkText(from, text, target) {
  const at = posix.normalize(posix.join(posix.dirname(from), target.split('#')[0]))
  return pageOf.has(at) && (text === target || text === posix.basename(at)) ? titleOf.get(at) : text
}

/** links rewrites the links of a line, whose text may hold `code`, but not those inside code. */
function links(line, from, to) {
  const code = [...line.matchAll(/`[^`]*`/g)].map((m) => [m.index, m.index + m[0].length])
  return line.replace(/\[((?:[^\]`]|`[^`]*`)*)\]\(([^)\s]+)\)/g, (whole, label, target, at) =>
    code.some(([start, end]) => at > start && at < end) ? whole : `[${linkText(from, label, target)}](${link(from, to, target)})`,
  )
}

/** rewrite turns a page of the repository into one of the site. */
export function rewrite(text, from, to) {
  const lang = to.startsWith('zh-CN/') ? 'zh-CN' : 'en'
  let fenced = false
  let alert = false
  const lines = []
  for (const [i, line] of text.split('\n').entries()) {
    if (/^\s*(```|~~~)/.test(line)) fenced = !fenced
    if (fenced) {
      lines.push(line)
      continue
    }
    // The link to the other language, alone on a line near the top, and
    // the one to this site.
    if (i < 10 && (/^\[(English|简体中文|中文)\]\([^)]+\)$/.test(line) || line.includes('getjec.dev'))) continue
    const start = /^> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]$/.exec(line)
    if (start) {
      const [kind, title] = alerts[lang][start[1]]
      lines.push(`::: ${kind} ${title}`)
      alert = true
      continue
    }
    let out = line
    if (alert) {
      if (out.startsWith('>')) {
        out = out.replace(/^> ?/, '')
      } else {
        lines.push(':::')
        alert = false
      }
    }
    lines.push(links(out, from, to))
  }
  if (alert) lines.push(':::')
  return lines.join('\n')
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const missing = unlisted()
  if (missing.length > 0) {
    console.error(`pages: not both in sections and in docs/: ${missing.join(', ')}`)
    process.exit(1)
  }
  rmSync(src, { recursive: true, force: true })
  for (const p of pages) {
    const out = join(src, p.to)
    const text = readFileSync(join(repo, p.from), 'utf8')
    mkdirSync(dirname(out), { recursive: true })
    writeFileSync(out, p.home ? text : rewrite(text, p.from, p.to))
  }
  cpSync(join(here, 'public'), join(src, 'public'), { recursive: true })
  console.log(`pages: ${pages.length} in ${src}`)
}
