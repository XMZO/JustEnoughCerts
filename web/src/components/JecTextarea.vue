<script setup lang="ts">
const model = defineModel<string>({ required: true })
withDefaults(defineProps<{ invalid?: boolean; rows?: number }>(), { rows: 4 })
</script>

<template>
  <textarea
    v-model="model"
    :rows="rows"
    :aria-invalid="invalid || undefined"
    spellcheck="false"
    autocapitalize="none"
    autocomplete="off"
    class="w-full resize-y rounded-md border border-line-strong bg-surface-raised px-3 py-2 font-mono text-small text-ink placeholder:text-ink-muted aria-invalid:border-danger-ink"
  />
</template>
