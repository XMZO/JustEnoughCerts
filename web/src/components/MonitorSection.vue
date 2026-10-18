<script setup lang="ts">
import { CircleCheck, Clock, Info, RefreshCw, TriangleAlert, Unplug } from '@lucide/vue'
import { computed, type Component } from 'vue'

import { useApiError } from '../api/errors'
import { useCheckNow, useMonitor, useMonitorOn, type MonitorResult } from '../api/monitor'
import { useT } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'

// The online check of a certificate (module monitor): what each domain
// served the last time it was connected to, with a way to check now. It
// shows only while the module runs.
const props = defineProps<{ id: number }>()
const { t } = useT()
const intl = useIntl()
const on = useMonitorOn()
const monitor = useMonitor(() => props.id)
const checkNow = useCheckNow(() => props.id)
const checkError = useApiError(computed(() => checkNow.error.value))

const body = computed(() => monitor.data.value)
const looks: Record<MonitorResult['state'], { icon: Component; tone: string }> = {
  ok: { icon: CircleCheck, tone: 'text-ok-ink' },
  old: { icon: TriangleAlert, tone: 'text-warn-ink' },
  other: { icon: Info, tone: 'text-ink-muted' },
  unreachable: { icon: Unplug, tone: 'text-ink-muted' },
}

function line(r: MonitorResult): string {
  const date = r.notAfter ? intl.date(r.notAfter) : ''
  switch (r.state) {
    case 'ok':
      return t('monitor.state.ok')
    case 'old':
      return t('monitor.state.old', { date })
    case 'other':
      return t('monitor.state.other', { issuer: r.issuer ?? '', date })
    case 'unreachable':
      return t('monitor.state.unreachable')
  }
  return ''
}
</script>

<template>
  <section v-if="on && body" aria-labelledby="monitor" class="flex flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 id="monitor" class="m-0 text-section-title font-semibold">{{ t('monitor.section.title') }}</h2>
      <JecButton v-if="body.tracked" size="sm" :busy="checkNow.isPending.value" @click="checkNow.mutate()">
        <RefreshCw class="size-4" aria-hidden="true" />{{ t('monitor.action.check') }}
      </JecButton>
    </div>
    <JecAlert v-if="checkError.error.value" :code="checkError.error.value.code" :params="checkError.error.value.params" />
    <p v-if="!body.tracked" class="m-0 text-small text-ink-muted">{{ t('monitor.section.untracked') }}</p>
    <template v-else>
      <p v-if="!body.results.length && body.checkAt" class="m-0 flex items-center gap-2 text-small text-ink-muted">
        <Clock class="size-4 flex-none" aria-hidden="true" />
        <span>{{ t('monitor.section.pending') }} <time :datetime="body.checkAt">{{ intl.dateTime(body.checkAt) }}</time></span>
      </p>
      <ul v-else class="m-0 flex list-none flex-col p-0">
        <li v-for="r in body.results" :key="r.domain" class="flex items-start gap-2 border-b border-line py-2 last:border-b-0">
          <component :is="looks[r.state].icon" :class="['mt-0.5 size-4 flex-none', looks[r.state].tone]" aria-hidden="true" />
          <div class="min-w-0 flex-1">
            <span class="block font-semibold [overflow-wrap:anywhere]" translate="no">{{ r.domain }}</span>
            <span class="block text-small">{{ line(r) }}</span>
            <span v-if="r.error" class="block font-mono text-caption text-ink-muted [overflow-wrap:anywhere]" translate="no">{{ r.error }}</span>
          </div>
          <time class="flex-none text-caption text-ink-muted tabular-nums" :datetime="r.checkedAt">{{ intl.dateTime(r.checkedAt) }}</time>
        </li>
      </ul>
      <p v-if="body.skipped.length" class="m-0 text-caption text-ink-muted">{{ t('monitor.section.skipped', { domains: body.skipped.join(', ') }) }}</p>
      <p v-if="body.results.length && body.checkAt" class="m-0 text-caption text-ink-muted">
        {{ t('monitor.section.next_check') }} <time :datetime="body.checkAt">{{ intl.dateTime(body.checkAt) }}</time>
      </p>
    </template>
  </section>
</template>
