<script setup lang="ts">
import { headPixels } from './creeper'
</script>

<template>
  <svg viewBox="0 0 8 8" shape-rendering="crispEdges" aria-hidden="true">
    <rect v-for="(p, i) in headPixels" :key="i" :x="p.x" :y="p.y" width="1" height="1" :fill="p.fill" />
  </svg>
</template>
