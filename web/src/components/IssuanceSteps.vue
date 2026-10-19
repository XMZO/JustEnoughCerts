<script setup lang="ts">
import { Circle, CircleCheck, LoaderCircle, OctagonX } from '@lucide/vue'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import type { IssuanceDetail } from '../api/certs'
import { useProviders } from '../api/providers'
import { deploySteps, stepStateText, stepText } from '../certs/issuance'
import { useT } from '../i18n'
import { useIntl } from '../i18n/intl'

// The steps of an issuance's last attempt, then the deployments of the
// version it brings, one for each target, each with its icon, its name and
// when it ended: on the issuance's page, and as a certificate's last run.
const props = defineProps<{ detail: IssuanceDetail }>()

const { t } = useT()
const intl = useIntl()
const targets = useProviders('target')

const icons = { pending: Circle, running: LoaderCircle, done: CircleCheck, failed: OctagonX } as const
const inks = { pending: 'text-ink-muted', running: 'text-info-ink', done: 'text-ok-ink', failed: 'text-danger-ink' } as const
const deploys = computed(() =>
  deploySteps(props.detail).map((d) => ({ ...d, name: targets.data.value?.find((p) => p.id === d.target)?.name ?? '' })),
)
</script>

<template>
  <ol class="m-0 flex list-none flex-col gap-1 p-0" :aria-label="t('certs.issuance.steps')">
    <li v-for="s in detail.steps" :key="s.step" class="grid grid-cols-[20px_minmax(0,1fr)_auto] items-center gap-2 py-1">
      <component :is="icons[s.state]" :class="['size-5', inks[s.state], s.state === 'running' ? 'animate-spin' : '']" aria-hidden="true" />
      <span :class="['min-w-0', s.state === 'pending' ? 'text-ink-muted' : '', s.state === 'running' ? 'font-semibold' : '']">
        {{ t(stepText[s.step]) }}
        <span class="sr-only">{{ t(stepStateText[s.state]) }}</span>
      </span>
      <time v-if="s.at" :datetime="s.at" class="text-caption text-ink-muted tabular-nums">{{ intl.time(s.at) }}</time>
    </li>
    <li v-for="d in deploys" :key="`deploy-${d.target}`" class="grid grid-cols-[20px_minmax(0,1fr)_auto] items-center gap-2 py-1">
      <component :is="icons[d.state]" :class="['size-5', inks[d.state], d.state === 'running' ? 'animate-spin' : '']" aria-hidden="true" />
      <span :class="['min-w-0 [overflow-wrap:anywhere]', d.state === 'pending' ? 'text-ink-muted' : '', d.state === 'running' ? 'font-semibold' : '']">
        <RouterLink v-if="d.link" :to="d.link" class="text-inherit">{{ t('certs.issuance.deploy_step', { target: d.name }) }}</RouterLink>
        <template v-else>{{ t('certs.issuance.deploy_step', { target: d.name }) }}</template>
        <span class="sr-only">{{ t(stepStateText[d.state]) }}</span>
      </span>
      <time v-if="d.at" :datetime="d.at" class="text-caption text-ink-muted tabular-nums">{{ intl.time(d.at) }}</time>
    </li>
  </ol>
</template>
