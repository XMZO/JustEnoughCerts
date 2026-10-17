<script setup lang="ts">
import { Search } from '@lucide/vue'

import { useT, type MessageKey } from '../i18n'

const model = defineModel<string>({ required: true })
defineProps<{ label: MessageKey }>()

const { t } = useT()
</script>

<template>
  <label class="relative block">
    <Search class="pointer-events-none absolute top-1/2 left-[11px] size-4 -translate-y-1/2 text-ink-muted" aria-hidden="true" />
    <input
      v-model="model"
      type="search"
      :aria-label="t(label)"
      :placeholder="t(label)"
      class="h-11 w-full rounded-md border border-line-strong bg-surface-raised pr-3 pl-[34px] text-ink placeholder:text-ink-muted sm:h-9"
    />
  </label>
</template>
