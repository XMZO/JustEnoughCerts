<script setup lang="ts">
import { CircleX } from '@lucide/vue'

import { useT, type MessageKey, type Params } from '../i18n'

defineProps<{ code: MessageKey; params?: Params }>()

const { t } = useT()
</script>

<template>
  <div role="alert" class="flex gap-2 rounded-md bg-danger-soft px-2.5 py-2 text-small font-semibold text-danger-ink">
    <CircleX class="mt-0.5 size-4 flex-none" aria-hidden="true" />
    <span>{{ t(code, params) }}</span>
  </div>
</template>
