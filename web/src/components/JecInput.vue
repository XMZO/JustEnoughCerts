<script setup lang="ts">
const model = defineModel<string>({ required: true })
defineProps<{ invalid?: boolean; mono?: boolean }>()
</script>

<template>
  <input
    v-model="model"
    :aria-invalid="invalid || undefined"
    :class="[
      'h-9 w-full rounded-md border border-line-strong bg-surface-raised px-3 text-ink aria-invalid:border-danger-ink',
      mono ? 'font-mono text-small' : '',
    ]"
  />
</template>
