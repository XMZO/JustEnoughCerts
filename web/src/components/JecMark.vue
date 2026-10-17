<script setup lang="ts">
// The pixel lock: one color, shown at whole multiples of 16px.
withDefaults(defineProps<{ size?: 16 | 32 | 48 | 64 }>(), { size: 32 })
</script>

<template>
  <svg :width="size" :height="size" viewBox="0 0 16 16" shape-rendering="crispEdges" aria-hidden="true">
    <path
      fill="#3c9a3f"
      fill-rule="evenodd"
      d="M5 1h6v1H5zM4 2h2v1H4zM10 2h2v1h-2zM3 3h2v4H3zM11 3h2v4h-2zM2 7h12v8H2zM7 9h2v4H7z"
    />
  </svg>
</template>
