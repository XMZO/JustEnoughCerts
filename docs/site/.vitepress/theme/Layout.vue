<script setup lang="ts">
import { useData, useRoute } from 'vitepress'
import DefaultTheme from 'vitepress/theme'
import { nextTick, provide, watch } from 'vue'

import HomePreview from './HomePreview.vue'

const { isDark } = useData()
const route = useRoute()

function motion(): boolean {
  return window.matchMedia('(prefers-reduced-motion: no-preference)').matches
}

// A page fades in as it opens: the first one by CSS (jec.css), as it is
// drawn, and the next ones here, as VitePress keeps the page's element.
watch(
  () => route.path,
  () =>
    nextTick(() => {
      if (!motion()) return
      document.querySelector('.VPDoc .content-container')?.animate(
        [
          { opacity: 0, translate: '0 8px' },
          { opacity: 1, translate: '0 0' },
        ],
        { duration: 450, easing: 'cubic-bezier(0.2, 0.8, 0.2, 1)' },
      )
    }),
)

// The switch of appearance reveals the new one in a circle that grows from
// the switch, where the browser has view transitions.
provide('toggle-appearance', async (event: MouseEvent) => {
  if (!('startViewTransition' in document) || !motion()) {
    isDark.value = !isDark.value
    return
  }
  // A key press has no pointer: the circle grows from the switch itself.
  let { clientX: x, clientY: y } = event
  if (x === 0 && y === 0 && event.currentTarget instanceof Element) {
    const box = event.currentTarget.getBoundingClientRect()
    x = box.left + box.width / 2
    y = box.top + box.height / 2
  }
  const radius = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y))
  const clipPath = [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`]
  await document.startViewTransition(async () => {
    isDark.value = !isDark.value
    await nextTick()
  }).ready
  document.documentElement.animate(
    { clipPath: isDark.value ? clipPath.reverse() : clipPath },
    {
      duration: 450,
      easing: 'cubic-bezier(0.4, 0, 0.2, 1)',
      fill: 'forwards',
      pseudoElement: `::view-transition-${isDark.value ? 'old' : 'new'}(root)`,
    },
  )
})
</script>

<template>
  <DefaultTheme.Layout>
    <template #home-features-after>
      <HomePreview />
    </template>
  </DefaultTheme.Layout>
</template>
