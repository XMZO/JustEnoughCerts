<script setup lang="ts">
import { CircleCheck, CircleX, TriangleAlert } from '@lucide/vue'

import type { CertificateState } from '../api/certs'
import { stateText } from '../certs/life'
import { useT } from '../i18n'

// The fixed look of each state (docs/design/system/components/StatusBadge.md):
// always an icon and a word, never color alone. With count, it says how
// many certificates are in that state.
defineProps<{ state: CertificateState; count?: number }>()

const { t } = useT()
const looks = {
  ok: { icon: CircleCheck, cls: 'bg-ok-soft text-ok-ink' },
  expiring: { icon: TriangleAlert, cls: 'bg-warn-soft text-warn-ink' },
  expired: { icon: CircleX, cls: 'bg-danger-soft text-danger-ink' },
} as const
</script>

<template>
  <span
    :class="[
      'inline-flex h-[22px] flex-none items-center gap-1 rounded-sm pr-2 pl-1.5 text-caption font-semibold whitespace-nowrap',
      looks[state].cls,
    ]"
  >
    <component :is="looks[state].icon" class="size-3.5 flex-none" aria-hidden="true" />
    {{ count === undefined ? t(stateText[state]) : t('certs.overview.badge', { label: t(stateText[state]), count }) }}
  </span>
</template>
