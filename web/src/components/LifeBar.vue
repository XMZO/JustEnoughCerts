<script setup lang="ts">
import { computed } from 'vue'

import { barPercent, lifeText, type Life } from '../certs/life'
import { useT } from '../i18n'

// The durability bar (docs/design/system/components/LifeBar.md): its
// length is the share of the validity left, its color the days left, and
// the days are always written next to it.
const props = defineProps<{
  life: Life
  /** The 88px bar with the days to its right, for table rows. */
  inline?: boolean
  /** The tall bar of the detail page. */
  large?: boolean
}>()

const { t } = useT()
const text = computed(() => lifeText(props.life))
const fills = { ok: 'bg-ok-fill', warn: 'bg-warn-fill', danger: 'bg-danger-fill' }
const labels = { ok: 'text-ink-muted', warn: 'font-semibold text-warn-ink', danger: 'font-semibold text-danger-ink' }
</script>

<template>
  <div :class="['flex min-w-16 gap-1', inline ? 'flex-row items-center gap-2' : 'flex-col-reverse']">
    <div
      :class="[
        'rounded-[1px] bg-bar-track p-px shadow-[0_0_0_1px_var(--bar-edge)]',
        inline ? 'w-[88px] flex-none' : '',
        large ? 'h-2.5' : 'h-1.5',
      ]"
    >
      <span :class="['block h-full', fills[life.level]]" :style="{ width: `${barPercent(life)}%` }" />
    </div>
    <span :class="['whitespace-nowrap tabular-nums', large ? 'text-heading' : 'text-small', labels[life.level]]">
      {{ t(text.key, text.params) }}
    </span>
  </div>
</template>
