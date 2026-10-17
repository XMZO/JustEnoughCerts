<script setup lang="ts">
import { useT, type MessageKey, type Params } from '../i18n'

/** A label, the input in the slot, and a hint or an error under it. */
defineProps<{
  id: string
  label: MessageKey
  hint?: MessageKey
  hintParams?: Params
  error?: MessageKey | null
  errorParams?: Params
}>()

const { t } = useT()
</script>

<template>
  <div class="grid gap-1.5">
    <label :for="id" class="text-small font-semibold">{{ t(label) }}</label>
    <slot />
    <p v-if="error" :id="`${id}-error`" class="m-0 text-small text-danger-ink">{{ t(error, errorParams) }}</p>
    <p v-else-if="hint" :id="`${id}-hint`" class="m-0 text-small text-ink-muted">{{ t(hint, hintParams) }}</p>
  </div>
</template>
