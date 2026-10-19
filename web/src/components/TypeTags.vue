<script setup lang="ts">
import { useT } from '../i18n'

// The marks of a provider type that may not work: experimental, as on an
// API that is not public, or untested, tried only against a stand-in for
// the service. The title says what each means.
defineProps<{ type?: { experimental?: boolean; untested?: boolean } }>()
const { t } = useT()
const tag = 'inline-block max-w-full flex-none truncate rounded-sm px-1.5 text-[11px] leading-[18px] font-semibold'
</script>

<template>
  <span v-if="type?.experimental" :class="[tag, 'bg-warn-soft text-warn-ink']" :title="t('kernel.modules.experimental')">
    {{ t('kernel.modules.experimental') }}
  </span>
  <span v-if="type?.untested" :class="[tag, 'bg-surface-sunken text-ink-muted']" :title="t('kernel.providers.untested_help')">
    {{ t('kernel.providers.untested') }}
  </span>
</template>
