<script setup lang="ts">
import { useData, withBase } from 'vitepress'
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

import source from '../../public/jec-hero.svg?raw'
import CreeperFace from './CreeperFace.vue'
import { dark, facePixels } from './creeper'

// The pixel lock of the home page, drawn from public/jec-hero.svg, with an
// easter egg in it: mined with clicks, as a block is in Minecraft, the lock
// cracks and opens, shows the face of a creeper, hisses and blows up, then
// mends. As a challenge of Minecraft does, it then shows a toast, says so
// in the chat and rewards experience, with the game's sounds (sounds.mjs
// fetches them for the build).

type Pixel = { x: number; y: number; fill: string }

// Each path of the SVG is one color, in rectangles one row high.
const pixels: Pixel[] = []
for (const [, fill, d] of source.matchAll(/<path fill="(#[0-9a-f]{6})" d="([^"]+)"/g)) {
  for (const [, x, y, w] of d.matchAll(/M(\d+) (\d+)h(\d+)v1H\d+z/g)) {
    for (let i = 0; i < Number(w); i++) pixels.push({ x: Number(x) + i, y: Number(y), fill })
  }
}
const label = /<title>([^<]*)<\/title>/.exec(source)?.[1] ?? 'Just Enough Certs'
// The body starts at row 7, under the shackle.
const top = 7
const shackle = pixels.filter((p) => p.y < top)
const body = pixels.filter((p) => p.y >= top)

// The face covers the middle of the body, so that the keyhole becomes the
// mouth; the rest of the keyhole turns green.
const left = 4
const isFace = (x: number, y: number) => facePixels.some((f) => f.x + left === x && f.y + top === y)
const face: Pixel[] = [
  ...facePixels.map((f) => ({ x: f.x + left, y: f.y + top, fill: dark })),
  ...body.filter((p) => p.fill === dark && !isFace(p.x, p.y)).map((p) => ({ ...p, fill: '#52b548' })),
]

// The cracks each hit adds, from the keyhole outwards. The hit after the
// last opens the lock.
const cracks = [
  [[6, 9], [9, 12]],
  [[5, 8], [10, 13]],
  [[6, 11], [9, 10]],
  [[4, 9], [11, 12]],
  [[5, 12], [10, 9], [3, 8]],
  [[12, 13], [4, 13], [11, 10], [3, 5]],
  [[12, 8], [3, 14], [13, 11], [12, 4], [2, 10]],
]

// The puffs of smoke of the blast.
const smoke = [
  { x: 7, y: 9, fill: '#d5dbd1' },
  { x: 5, y: 10, fill: '#a1ab9c' },
  { x: 9, y: 8, fill: '#d5dbd1' },
  { x: 10, y: 11, fill: '#a1ab9c' },
  { x: 6, y: 12, fill: '#6b7766' },
  { x: 8, y: 11, fill: '#d5dbd1' },
  { x: 7, y: 6, fill: '#a1ab9c' },
]

const stage = ref(0)
const open = ref(false)
const creeper = ref(false)
const busy = ref(false)
const shown = computed(() => cracks.slice(0, stage.value).flat().map(([x, y]) => ({ x, y })))
// The shackle rises as the lock gives: a pixel halfway, two once open.
const lift = computed(() => (open.value ? 2 : stage.value * 2 > cracks.length ? 1 : 0))

const svg = ref<SVGSVGElement>()
const all = ref<SVGGElement>()
const toast = ref(false)
const chat = ref(false)
const mounted = ref(false)
const { lang } = useData()
const zh = computed(() => lang.value.startsWith('zh'))
const challenge = computed(() => (zh.value ? '嘶嘶嘶……证书毫发无损' : 'Sssss… your certs are still green'))

// The experience the challenges have rewarded on this page, and the bar
// that shows it, as above the hotbar.
const xp = ref(false)
const level = ref(0)
const progress = ref(0)
const snap = ref(false)

let still = false
let timer: ReturnType<typeof setTimeout> | undefined
let toastTimer: ReturnType<typeof setTimeout> | undefined
let chatTimer: ReturnType<typeof setTimeout> | undefined

onMounted(() => {
  mounted.value = true
  still = matchMedia('(prefers-reduced-motion: reduce)').matches
})

onBeforeUnmount(() => {
  clearTimeout(timer)
  clearTimeout(toastTimer)
  clearTimeout(chatTimer)
})

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

// The sounds play through one AudioContext, which the first hit starts, as
// browsers let pages play sound only after the visitor did something. A
// sound that does not load or decode, as Ogg on some browsers, stays
// silent.
const sounds = ['fuse', 'explode1', 'explode2', 'explode3', 'explode4', 'advancement', 'levelup']
let audio: AudioContext | undefined
const buffers = new Map<string, Promise<AudioBuffer | undefined>>()

function listen() {
  if (!audio && 'AudioContext' in window) {
    audio = new AudioContext()
    for (const name of sounds) {
      const ctx = audio
      buffers.set(
        name,
        fetch(withBase(`/sounds/${name}.ogg`))
          .then((res) => (res.ok ? res.arrayBuffer() : Promise.reject(new Error(res.statusText))))
          .then((data) => ctx.decodeAudioData(data))
          .catch(() => undefined),
      )
    }
  }
  void audio?.resume()
}

// play starts a sound and returns what stops it, fading it out at once.
async function play(name: string, volume: number): Promise<(() => void) | undefined> {
  const buffer = await buffers.get(name)
  const ctx = audio
  if (!ctx || !buffer) return
  const source = ctx.createBufferSource()
  const gain = ctx.createGain()
  source.buffer = buffer
  gain.gain.value = volume
  source.connect(gain).connect(ctx.destination)
  source.start()
  return () => {
    gain.gain.setTargetAtTime(0, ctx.currentTime, 0.02)
    source.stop(ctx.currentTime + 0.1)
  }
}

// The hiss of the fuse, which the blast cuts short.
let hiss: Promise<(() => void) | undefined> | undefined

// Mining lets up when the clicks stop: the cracks close one by one.
function mend() {
  if (stage.value === 0 || busy.value) return
  stage.value--
  timer = setTimeout(mend, 250)
}

function hit() {
  if (busy.value) return
  listen()
  clearTimeout(timer)
  if (stage.value < cracks.length) {
    stage.value++
    if (!still) {
      all.value?.animate(
        [{ translate: '0 0' }, { translate: '-0.2px 0' }, { translate: '0.2px 0' }, { translate: '0 0' }],
        { duration: 150 },
      )
    }
    timer = setTimeout(mend, 1200)
    return
  }
  void blowUp()
}

async function blowUp() {
  busy.value = true
  // The glint over the lock (jec.css) waits.
  const container = svg.value?.closest('.image-container')
  container?.classList.add('jec-egg')
  open.value = true
  creeper.value = true
  if (still) {
    await wait(1600)
    reset()
  } else {
    await wait(400)
    await fuse()
    const flights = await explode()
    reset()
    await nextTick()
    await fly(flights)
  }
  container?.classList.remove('jec-egg')
  busy.value = false
  toast.value = true
  chat.value = true
  void play('advancement', 0.35)
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (toast.value = false), 5000)
  clearTimeout(chatTimer)
  chatTimer = setTimeout(() => (chat.value = xp.value = false), 10000)
  void reward(100)
}

// The points a level takes, as in Minecraft.
const needs = (n: number) => (n < 16 ? 2 * n + 7 : n < 31 ? 5 * n - 38 : 9 * n - 158)

// reward adds experience, the bar filling level after level; every fifth
// level rings.
async function reward(gained: number) {
  xp.value = true
  let left = gained
  while (left > 0) {
    const need = needs(level.value)
    const step = Math.min(left, need - Math.round(progress.value * need))
    left -= step
    progress.value += step / need
    if (!still) await wait(130)
    if (progress.value >= 1 - 1e-9) {
      snap.value = true
      level.value++
      progress.value = 0
      await nextTick()
      snap.value = false
      if (level.value % 5 === 0 && !still) void play('levelup', 0.5)
    }
  }
}

function reset() {
  open.value = false
  creeper.value = false
  stage.value = 0
}

// Swelling, the creeper flashes white three times before it blows.
async function fuse() {
  hiss = play('fuse', 0.6)
  all.value?.animate([{ scale: '1' }, { scale: '1.15' }], { duration: 1500, easing: 'ease-in', fill: 'forwards' })
  const flashes = [...(svg.value?.querySelectorAll('.flash') ?? [])].map((el) =>
    el.animate([{ opacity: 0 }, { opacity: 0.85 }, { opacity: 0 }], { duration: 500, iterations: 3 }),
  )
  await Promise.all(flashes.map((a) => a.finished))
}

type Flight = { el: Element; to: string; blast: Animation }

// Every pixel flies off and fades, as smoke puffs up where the lock was.
async function explode(): Promise<Flight[]> {
  for (const a of all.value?.getAnimations() ?? []) a.cancel()
  void hiss?.then((stop) => stop?.())
  void play(`explode${1 + Math.floor(Math.random() * 4)}`, 0.6)
  for (const [i, el] of [...(svg.value?.querySelectorAll('.smoke rect') ?? [])].entries()) {
    el.animate(
      [
        { opacity: 0.9, scale: '0.6' },
        { opacity: 0, scale: '2.6' },
      ],
      { duration: 800, delay: i * 40, easing: 'ease-out' },
    )
  }
  const flights = [...(svg.value?.querySelectorAll('.px') ?? [])].map((el) => {
    const x = Number(el.getAttribute('x')) + 0.5 - 8
    const y = Number(el.getAttribute('y')) + 0.5 - 9
    const angle = Math.atan2(y, x) + (Math.random() - 0.5) * 0.8
    const distance = 5 + Math.random() * 9
    const to = `${Math.cos(angle) * distance}px ${Math.sin(angle) * distance + 3}px`
    const blast = el.animate(
      [
        { translate: '0 0', rotate: '0deg', opacity: 1 },
        { translate: to, rotate: `${(Math.random() - 0.5) * 540}deg`, opacity: 0 },
      ],
      { duration: 700 + Math.random() * 300, easing: 'cubic-bezier(0.15, 0.6, 0.3, 1)', fill: 'forwards' },
    )
    return { el, to, blast }
  })
  await Promise.all(flights.map((f) => f.blast.finished))
  await wait(300)
  return flights
}

// The pixels of the lock come back to their places; those of the face and
// the cracks are gone.
async function fly(flights: Flight[]) {
  const back = flights
    .filter((f) => f.el.isConnected)
    .map((f, i) => {
      const a = f.el.animate(
        [
          { translate: f.to, opacity: 0 },
          { translate: '0 0', opacity: 1 },
        ],
        { duration: 500, delay: (i % 16) * 12, easing: 'cubic-bezier(0.5, 0, 0.2, 1)', fill: 'backwards' },
      )
      f.blast.cancel()
      return a
    })
  await Promise.all(back.map((a) => a.finished))
}
</script>

<template>
  <svg
    ref="svg"
    class="image-src jec-lock"
    viewBox="0 0 16 16"
    role="img"
    :aria-label="label"
    shape-rendering="crispEdges"
    @click="hit"
  >
    <g ref="all" class="all">
      <g :transform="`translate(0 ${-lift})`">
        <rect v-for="(p, i) in shackle" :key="`s${i}`" class="px" :x="p.x" :y="p.y" width="1" height="1" :fill="p.fill" />
        <template v-for="c in shown" :key="`c${c.x},${c.y}`">
          <rect v-if="c.y < top" class="px" :x="c.x" :y="c.y" width="1" height="1" fill="#000" fill-opacity="0.35" />
        </template>
        <g class="flash" opacity="0">
          <rect v-for="(p, i) in shackle" :key="`sf${i}`" :x="p.x" :y="p.y" width="1" height="1" fill="#fff" />
        </g>
      </g>
      <g>
        <rect v-for="(p, i) in body" :key="`b${i}`" class="px" :x="p.x" :y="p.y" width="1" height="1" :fill="p.fill" />
        <template v-for="c in shown" :key="`c${c.x},${c.y}`">
          <rect v-if="c.y >= top" class="px" :x="c.x" :y="c.y" width="1" height="1" fill="#000" fill-opacity="0.35" />
        </template>
        <template v-if="creeper">
          <rect v-for="(p, i) in face" :key="`f${i}`" class="px" :x="p.x" :y="p.y" width="1" height="1" :fill="p.fill" />
        </template>
        <g class="flash" opacity="0">
          <rect v-for="(p, i) in body" :key="`bf${i}`" :x="p.x" :y="p.y" width="1" height="1" fill="#fff" />
        </g>
      </g>
      <g class="smoke">
        <rect v-for="(s, i) in smoke" :key="`k${i}`" :x="s.x" :y="s.y" width="2" height="2" :fill="s.fill" opacity="0" />
      </g>
    </g>
  </svg>
  <Teleport v-if="mounted" to="body">
    <div class="jec-toast-region" role="status">
      <Transition name="jec-toast">
        <div v-if="toast" class="jec-toast">
          <CreeperFace class="icon" />
          <div>
            <p class="title">{{ zh ? '挑战已完成！' : 'Challenge Complete!' }}</p>
            <p class="text">{{ challenge }}</p>
          </div>
        </div>
      </Transition>
    </div>
    <Transition name="jec-fade">
      <p v-if="chat" class="jec-chat" aria-hidden="true">
        {{ zh ? '你完成了挑战' : 'You have completed the challenge ' }}<span class="name">[{{ challenge }}]</span>
      </p>
    </Transition>
    <Transition name="jec-fade">
      <div v-if="xp" class="jec-xp" aria-hidden="true">
        <span class="level">{{ level }}</span>
        <div class="bar"><div class="fill" :class="{ snap }" :style="{ width: `${progress * 100}%` }" /></div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.jec-lock {
  overflow: visible;
  user-select: none;
  -webkit-user-select: none;
  touch-action: manipulation;
  -webkit-tap-highlight-color: transparent;
}

.all {
  transform-box: view-box;
  transform-origin: 50% 50%;
}

.px,
.smoke rect {
  transform-box: fill-box;
  transform-origin: center;
}

/* As a challenge is announced in Minecraft. */
.jec-toast {
  position: fixed;
  top: calc(var(--vp-nav-height) + 16px);
  right: 16px;
  z-index: 100;
  display: flex;
  gap: 12px;
  align-items: center;
  max-width: calc(100vw - 32px);
  padding: 10px 18px 10px 10px;
  border: 2px solid #555;
  border-radius: 4px;
  background: #212121;
  box-shadow:
    inset 2px 2px 0 rgb(255 255 255 / 0.12),
    inset -2px -2px 0 rgb(0 0 0 / 0.4),
    0 8px 24px rgb(0 0 0 / 0.3);
  color: #fff;
  font-size: 14px;
  line-height: 1.4;
}

.jec-toast .icon {
  flex: none;
  width: 32px;
  height: 32px;
}

.jec-toast p {
  margin: 0;
}

.jec-toast .title {
  color: #ff88ff;
  font-weight: 600;
}

/* A line of the chat, at the bottom left. */
.jec-chat {
  position: fixed;
  left: 16px;
  bottom: calc(72px + env(safe-area-inset-bottom, 0px));
  z-index: 100;
  max-width: calc(100vw - 32px);
  margin: 0;
  padding: 2px 6px;
  background: rgb(0 0 0 / 0.75);
  /* The page behind would show through, unlike a game world. */
  backdrop-filter: blur(6px);
  color: #fff;
  font-size: 14px;
  line-height: 1.5;
  text-shadow: 2px 2px 0 #3f3f3f;
  pointer-events: none;
}

.jec-chat .name {
  color: #aa00aa;
  text-shadow: 2px 2px 0 #2a002a;
}

/* The experience bar, at the bottom in the middle, its level above it. */
.jec-xp {
  position: fixed;
  left: 50%;
  bottom: calc(24px + env(safe-area-inset-bottom, 0px));
  z-index: 100;
  width: min(364px, calc(100vw - 32px));
  transform: translateX(-50%);
  pointer-events: none;
}

.jec-xp .level {
  position: absolute;
  bottom: 14px;
  left: 50%;
  transform: translateX(-50%);
  color: #80ff20;
  font-size: 18px;
  font-weight: 700;
  line-height: 1;
  text-shadow:
    2px 0 #000,
    -2px 0 #000,
    0 2px #000,
    0 -2px #000;
}

.jec-xp .bar {
  height: 10px;
  border: 2px solid #000;
  background: #1f3a0f;
}

.jec-xp .fill {
  height: 100%;
  background: linear-gradient(#b4ff5a, #6fd51f);
  transition: width 0.12s linear;
}

.jec-xp .fill.snap {
  transition: none;
}

.jec-fade-enter-active,
.jec-fade-leave-active {
  transition: opacity 0.6s;
}

.jec-fade-enter-from,
.jec-fade-leave-to {
  opacity: 0;
}

.jec-toast-enter-active,
.jec-toast-leave-active {
  transition:
    transform 0.45s cubic-bezier(0.2, 0.8, 0.2, 1),
    opacity 0.45s;
}

.jec-toast-enter-from,
.jec-toast-leave-to {
  transform: translateX(calc(100% + 32px));
  opacity: 0;
}

@media (prefers-reduced-motion: reduce) {
  .jec-toast-enter-from,
  .jec-toast-leave-to {
    transform: none;
  }

  .jec-xp .fill {
    transition: none;
  }
}
</style>
