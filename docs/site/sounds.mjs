// Fetches the sounds of the home page's easter egg (.vitepress/theme/
// HeroLock.vue) into public/sounds, from Minecraft's asset server, each
// checked against its SHA-1 there. They are Mojang's, not under this
// repository's license, so every build of the site fetches them and the
// repository never holds them.
//
//	node sounds.mjs
//
// They wait in node_modules/.cache/jec-sounds once downloaded.
import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const out = join(here, 'public', 'sounds')
const cache = join(here, 'node_modules', '.cache', 'jec-sounds')

// The sounds by their names on the site, with their files in Minecraft's
// asset index 34 (Minecraft 26.3) and their SHA-1.
const sounds = {
  fuse: ['random/fuse.ogg', 'b9a550ba1e028c027557572aeb928b760864efe4'],
  explode1: ['random/explode1.ogg', 'fa85b9e7e54c36c9f9d0249ca739444b5e3bb66c'],
  explode2: ['random/explode2.ogg', 'e55daa373e577f611565d57ec53cd28dcf472f99'],
  explode3: ['random/explode3.ogg', '3a92d0fd263265719532041f6f2ea44c7ba2f2e2'],
  explode4: ['random/explode4.ogg', '09835e0c3138faf6047506a2b6f1da087c082d2e'],
  advancement: ['ui/toast/challenge_complete.ogg', 'bd01dff39a7bd1e0e7f6847f7aee981f157e4f94'],
  levelup: ['random/levelup.ogg', '19034765ba8ba5389b35804ab213537ab5cf706f'],
}

const sha1 = (data) => createHash('sha1').update(data).digest('hex')

mkdirSync(out, { recursive: true })
mkdirSync(cache, { recursive: true })
for (const [name, [file, hash]] of Object.entries(sounds)) {
  const cached = join(cache, hash)
  if (!existsSync(cached)) {
    const url = `https://resources.download.minecraft.net/${hash.slice(0, 2)}/${hash}`
    const res = await fetch(url)
    if (!res.ok) throw new Error(`${file}: ${url}: ${res.status}`)
    const data = Buffer.from(await res.arrayBuffer())
    if (sha1(data) !== hash) throw new Error(`${file}: SHA-1 ${sha1(data)}, want ${hash}`)
    writeFileSync(cached, data)
  }
  writeFileSync(join(out, `${name}.ogg`), readFileSync(cached))
}
