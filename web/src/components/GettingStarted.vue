<script setup lang="ts">
import { Circle, CircleCheck, FileDown, Plus } from '@lucide/vue'
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import { useProviders, type ProviderKind } from '../api/providers'
import { useT, type MessageKey } from '../i18n'
import JecButton from './JecButton.vue'

// What the overview shows before the first certificate: the providers to
// set up, in the order a certificate needs them, each with how many there
// are, and the ways to add one.
const { t } = useT()

const sources = useProviders('source')
const solvers = useProviders('solver')
const targets = useProviders('target')
const notifiers = useProviders('notifier')

interface Step {
  kind: ProviderKind
  to: string
  count: number | undefined
  optional: boolean
}

const steps = computed<Step[]>(() => [
  { kind: 'source', to: '/sources', count: sources.data.value?.length, optional: false },
  { kind: 'solver', to: '/dns', count: solvers.data.value?.length, optional: false },
  { kind: 'target', to: '/targets', count: targets.data.value?.length, optional: true },
  { kind: 'notifier', to: '/notify', count: notifiers.data.value?.length, optional: true },
])

const title = (kind: ProviderKind) => `kernel.providers.${kind}.title` as MessageKey
const intro = (kind: ProviderKind) => `kernel.providers.${kind}.intro` as MessageKey
</script>

<template>
  <section aria-labelledby="getting-started" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
    <h2 id="getting-started" class="m-0 text-section-title font-semibold">{{ t('certs.overview.start.title') }}</h2>
    <p class="m-0 text-ink-muted">{{ t('certs.overview.start.intro') }}</p>
    <ol class="m-0 grid list-none gap-2 p-0">
      <li v-for="s in steps" :key="s.kind" class="grid min-w-0">
        <RouterLink
          :to="s.to"
          class="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-3 rounded-md border border-line px-3 py-2.5 text-ink no-underline hover:bg-surface-sunken hover:text-ink"
        >
          <component :is="s.count ? CircleCheck : Circle" :class="['mt-0.5 size-5 flex-none', s.count ? 'text-ok-ink' : 'text-ink-muted']" aria-hidden="true" />
          <span class="grid min-w-0 gap-0.5">
            <span class="flex flex-wrap items-baseline justify-between gap-x-3">
              <span class="font-semibold">
                {{ t(title(s.kind)) }}<small v-if="s.optional" class="ml-2 text-small font-normal text-ink-muted">{{ t('certs.overview.start.optional') }}</small>
              </span>
              <span v-if="s.count !== undefined" :class="['text-small whitespace-nowrap', s.count ? 'font-semibold text-ok-ink' : 'text-ink-muted']">
                {{ s.count ? t('certs.overview.start.added', { count: s.count }) : t('certs.overview.start.todo') }}
              </span>
            </span>
            <span class="text-small text-ink-muted">{{ t(intro(s.kind)) }}</span>
          </span>
        </RouterLink>
      </li>
    </ol>
    <div class="flex flex-wrap gap-2">
      <JecButton variant="primary" to="/certificates/new"><Plus class="size-4" aria-hidden="true" />{{ t('certs.action.add') }}</JecButton>
      <JecButton :to="{ path: '/certificates/new', query: { source: 'upload' } }"><FileDown class="size-4" aria-hidden="true" />{{ t('certs.action.import') }}</JecButton>
    </div>
  </section>
</template>
