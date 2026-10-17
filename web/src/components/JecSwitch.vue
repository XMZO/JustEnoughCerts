<script setup lang="ts">
// An on/off switch that acts at once, with no Save to follow
// (docs/design/system/components/Switch.md). It shows no text: the caller
// names it, and puts the words beside it.
defineProps<{ label: string; disabled?: boolean }>()
const on = defineModel<boolean>({ required: true })
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="on ? 'true' : 'false'"
    :aria-label="label"
    :disabled="disabled"
    :class="[
      'relative h-5 w-9 flex-none cursor-pointer rounded-full border-0 p-0 transition-colors duration-150',
      'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus-ring disabled:cursor-not-allowed disabled:opacity-50',
      on ? 'bg-brand' : 'bg-line-strong',
    ]"
    @click="on = !on"
  >
    <span
      :class="[
        'absolute top-0.5 left-0.5 size-4 rounded-full bg-surface-raised shadow-[0_1px_2px_rgb(0_0_0/0.25)] transition-transform duration-150',
        on ? 'translate-x-4' : '',
      ]"
      aria-hidden="true"
    />
  </button>
</template>
