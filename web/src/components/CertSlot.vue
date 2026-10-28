<script setup lang="ts">
import { computed } from 'vue'

import type { Certificate } from '../api/certs'
import { barPercent, certLife, lifeText, mono, stateText } from '../certs/life'
import { useT } from '../i18n'

// An inventory slot (docs/design/system/components/CertSlot.md): two
// letters of the domain and a small durability bar, 48px on phones and 56px
// from 640px. The label says it all for screen readers; a decorative slot,
// such as the one beside a page title, is a picture only.
const props = defineProps<{ cert: Certificate; pressed?: boolean; decorative?: boolean }>()
defineEmits<{ select: [] }>()

const { t } = useT()
const life = computed(() => certLife(props.cert))
const label = computed(() => {
  const l = life.value
  const text = l ? lifeText(l) : null
  return t('certs.cert.label', {
    domain: props.cert.domains[0] ?? '',
    state: t(stateText[props.cert.state]),
    life: text ? t(text.key, text.params) : '',
  })
})
const fills = { ok: 'bg-ok-fill', warn: 'bg-warn-fill', danger: 'bg-danger-fill' }
</script>

<template>
  <component
    :is="decorative ? 'span' : 'button'"
    :type="decorative ? undefined : 'button'"
    :aria-pressed="decorative ? undefined : pressed ? 'true' : 'false'"
    :aria-label="decorative ? undefined : label"
    :aria-hidden="decorative ? 'true' : undefined"
    :class="[
      'relative grid size-12 flex-none place-items-center rounded-sm border-0 bg-surface-sunken p-0 text-[15px] font-bold tracking-[-0.02em] text-ink shadow-slot sm:size-14 sm:text-[17px]',
      decorative ? '' : 'cursor-pointer hover:bg-brand-soft aria-pressed:outline-2 aria-pressed:outline-offset-2 aria-pressed:outline-brand',
    ]"
    @click="decorative || $emit('select')"
  >
    <!-- Drawn by CSS: two letters are a picture, not a name to speak. -->
    <span class="-translate-y-[3px] before:content-[attr(data-mono)]" :data-mono="mono(cert.domains[0] ?? '')" aria-hidden="true" />
    <span
      v-if="life"
      class="absolute right-1.5 bottom-[5px] left-1.5 h-1 bg-bar-track p-px shadow-[0_0_0_1px_var(--bar-edge)] sm:right-[7px] sm:bottom-1.5 sm:left-[7px]"
      aria-hidden="true"
    >
      <span :class="['block h-full', fills[life.level]]" :style="{ width: `${barPercent(life)}%` }" />
    </span>
  </component>
</template>
