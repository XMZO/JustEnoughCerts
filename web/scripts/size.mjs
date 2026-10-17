// Prints the size of what the first page load downloads and fails when the
// JavaScript in it, gzipped, is over budget (PLAN.md 6.4).
import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { gzipSync } from 'node:zlib'

const budget = 150 * 1024
const dist = new URL('../dist/', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const html = readFileSync(join(dist, 'index.html'), 'utf8')

// The entry script, the chunks it preloads and the stylesheets.
const refs = [...html.matchAll(/<(?:script[^>]*\ssrc|link[^>]*\shref)="\.\/([^"]+\.(?:js|css))"/g)].map((m) => m[1])

const gz = (file) => {
  const pre = join(dist, `${file}.gz`)
  return existsSync(pre) ? readFileSync(pre).length : gzipSync(readFileSync(join(dist, file)), { level: 9 }).length
}
const kib = (n) => `${(n / 1024).toFixed(1)} KiB`

let js = 0
const rows = []
for (const file of refs) {
  const size = gz(file)
  if (file.endsWith('.js')) js += size
  rows.push(`| ${file} | ${kib(size)} |`)
}
console.log('| First load (gzip) | Size |\n|---|---:|')
console.log(rows.join('\n'))
console.log(`| JavaScript | ${kib(js)} of ${kib(budget)} |`)
if (js > budget) {
  console.error(`first-load JavaScript is ${kib(js)}, over the ${kib(budget)} budget`)
  process.exit(1)
}
