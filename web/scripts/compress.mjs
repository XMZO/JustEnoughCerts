// Writes .br and .gz copies of the compressible files in dist/, so that the
// server sends them as they are instead of compressing on every request.
import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { brotliCompressSync, constants, gzipSync } from 'node:zlib'

const dist = new URL('../dist/', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')
const compressible = /\.(?:js|css|html|svg|json|txt|map)$/
const minSize = 1024

function* files(dir) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) yield* files(path)
    else if (compressible.test(name)) yield path
  }
}

let count = 0
for (const path of files(dist)) {
  const data = readFileSync(path)
  if (data.length < minSize) continue
  writeFileSync(`${path}.br`, brotliCompressSync(data, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }))
  writeFileSync(`${path}.gz`, gzipSync(data, { level: 9 }))
  count++
}
console.log(`compressed ${count} files`)

// Vite empties dist/; put back the placeholder that keeps go:embed working
// in a checkout where the UI is not built.
writeFileSync(join(dist, '.gitkeep'), '')
