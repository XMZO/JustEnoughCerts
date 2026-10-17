<script setup lang="ts">
import { Check, Copy } from '@lucide/vue'
import { onBeforeUnmount, ref } from 'vue'

import { useT, type MessageKey } from '../i18n'

// An icon button that copies value, and says so for a moment.
const props = defineProps<{ value: string; label: MessageKey }>()

const { t } = useT()
const copied = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

async function copy() {
  try {
    await navigator.clipboard.writeText(props.value)
  } catch {
    return // no clipboard access: nothing to report beyond the unchanged icon
  }
  copied.value = true
  clearTimeout(timer)
  timer = setTimeout(() => (copied.value = false), 2000)
}

onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <button
    type="button"
    :aria-label="t(label)"
    :title="t(label)"
    class="grid size-6 flex-none cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink-muted hover:bg-surface-sunken hover:text-ink"
    @click="copy"
  >
    <Check v-if="copied" class="size-4 text-ok-ink" aria-hidden="true" />
    <Copy v-else class="size-4" aria-hidden="true" />
  </button>
  <span class="sr-only" aria-live="polite"><template v-if="copied">{{ t('kernel.action.copied') }}</template></span>
</template>
