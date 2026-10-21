<script setup lang="ts">
import { CircleCheck, Clock, Info, Pencil, RefreshCw, TriangleAlert, Unplug } from '@lucide/vue'
import { computed, ref, watch, type Component } from 'vue'

import { useApiError } from '../api/errors'
import { useCheckNow, useMonitor, useMonitorOn, useSetPorts, type MonitorResult } from '../api/monitor'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'

// The online check of a certificate (module monitor): what each domain
// served the last time it was connected to, and whether without its
// intermediates, with a way to check now; the
// domains that served the same, as visitors from the same side see them,
// share a row. It shows only while the module runs.
const props = defineProps<{ id: number }>()
const { t } = useT()
const intl = useIntl()
const on = useMonitorOn()
const monitor = useMonitor(() => props.id)
const checkNow = useCheckNow(() => props.id)
const checkError = useApiError(computed(() => checkNow.error.value))

// The ports the domains are checked on: 443, or those of a mail server.
const editingPorts = ref(false)
const portsText = ref('')
const setPorts = useSetPorts(() => props.id)
const portsError = useApiError(computed(() => setPorts.error.value))
const portsInvalid = ref(false)
watch(editingPorts, (now) => {
  if (!now) return
  setPorts.reset()
  portsInvalid.value = false
  portsText.value = (monitor.data.value?.ports ?? [443]).join(', ')
})
async function savePorts() {
  const ports = portsText.value.split(/[\s,\uff0c]+/).filter(Boolean).map(Number)
  portsInvalid.value = !ports.length || ports.some((p) => !Number.isInteger(p) || p < 1 || p > 65535)
  if (portsInvalid.value) return
  try {
    await setPorts.mutateAsync(ports)
  } catch {
    return // shown by the alert
  }
  editingPorts.value = false
}
const ports = computed(() => monitor.data.value?.ports ?? [443])
const otherPorts = computed(() => ports.value.length !== 1 || ports.value[0] !== 443)

const body = computed(() => monitor.data.value)
const looks: Record<MonitorResult['state'], { icon: Component; tone: string }> = {
  ok: { icon: CircleCheck, tone: 'text-ok-ink' },
  old: { icon: TriangleAlert, tone: 'text-warn-ink' },
  other: { icon: Info, tone: 'text-ink-muted' },
  unreachable: { icon: Unplug, tone: 'text-ink-muted' },
}

// A domain whose DNS sends visitors from mainland China and from elsewhere
// apart is checked for each of them.
const views: Record<NonNullable<MonitorResult['view']>, MessageKey> = {
  cn: 'monitor.view.cn',
  global: 'monitor.view.global',
}

// The results, one row for each outcome, with the domains it was for and
// the last time it was seen.
const rows = computed(() => {
  const out: { key: string; domains: string[]; result: MonitorResult; at: string }[] = []
  for (const r of body.value?.results ?? []) {
    const key = [r.view, r.state, r.fingerprint, r.issuer, r.notAfter, r.error, r.incomplete, r.tlsVersion, r.legacyTls].map((x) => x ?? '').join('|')
    const row = out.find((x) => x.key === key)
    if (!row) out.push({ key, domains: [r.domain], result: r, at: r.checkedAt })
    else {
      row.domains.push(r.domain)
      if (Date.parse(r.checkedAt) > Date.parse(row.at)) row.at = r.checkedAt
    }
  }
  return out
})

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
      if (r.cause === 'dns') return t('monitor.state.no_dns')
      if (r.cause === 'connect') return t('monitor.state.no_connect', { port: r.port })
      return t('monitor.state.unreachable', { port: r.port })
  }
  return ''
}
</script>

<template>
  <!-- The next check: beside the title where the card is wide, under the results where it is narrow. -->
  <section v-if="on && body" aria-labelledby="monitor" class="@container flex flex-col gap-1.5 rounded-lg border border-line bg-surface-raised p-3 sm:p-4">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
      <h2 id="monitor" class="m-0 mr-auto text-heading font-semibold sm:text-section-title">{{ t('monitor.section.title') }}</h2>
      <span v-if="body.results.length && body.checkAt" class="hidden text-caption text-ink-muted @md:inline">
        {{ t('monitor.section.next_check') }} <time :datetime="body.checkAt">{{ intl.dateTime(body.checkAt) }}</time>
      </span>
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
        <li v-for="row in rows" :key="row.key" class="flex items-start gap-2 border-b border-line py-1.5 last:border-b-0">
          <component
            :is="row.result.incomplete ? TriangleAlert : looks[row.result.state].icon"
            :class="['mt-0.5 size-4 flex-none', row.result.incomplete ? 'text-warn-ink' : looks[row.result.state].tone]"
            aria-hidden="true"
          />
          <div class="min-w-0 flex-1">
            <span class="flex flex-wrap items-center gap-x-2">
              <span v-for="d in row.domains" :key="d" class="font-semibold [overflow-wrap:anywhere]" translate="no">{{ d }}</span>
              <span v-if="row.result.view" class="rounded-sm bg-surface-sunken px-1.5 text-caption text-ink-muted">{{ t(views[row.result.view]) }}</span>
            </span>
            <span class="flex flex-wrap items-baseline justify-between gap-x-3 text-small">
              <span>
                {{ line(row.result) }}
                <span v-if="row.result.tlsVersion" class="text-caption text-ink-muted">{{ t('monitor.tls.version', { version: row.result.tlsVersion }) }}</span>
              </span>
              <time class="text-caption text-ink-muted tabular-nums" :datetime="row.at">{{ intl.dateTime(row.at) }}</time>
            </span>
            <span v-if="row.result.incomplete" class="block text-small font-semibold text-warn-ink">{{ t('monitor.state.incomplete') }}</span>
            <span v-if="row.result.legacyTls" class="block text-small text-warn-ink">{{ t('monitor.tls.legacy') }}</span>
            <span v-if="row.result.error" class="block font-mono text-caption text-ink-muted [overflow-wrap:anywhere]" translate="no">{{ row.result.error }}</span>
          </div>
        </li>
      </ul>
      <p v-if="body.skipped.length" class="m-0 text-caption text-ink-muted">{{ t('monitor.section.skipped', { domains: body.skipped.join(', ') }) }}</p>
      <p class="m-0 flex items-center gap-1.5 text-caption text-ink-muted">
        <span>{{ t('monitor.ports.label') }}</span>
        <data :value="ports.join(',')" :class="otherPorts ? 'font-semibold text-ink' : ''" translate="no">{{ ports.join(', ') }}</data>
        <button
          type="button"
          :aria-label="t('monitor.ports.edit')"
          class="grid size-6 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-ink-muted hover:bg-surface-sunken hover:text-ink max-sm:size-9"
          @click="editingPorts = true"
        >
          <Pencil class="size-3.5" aria-hidden="true" />
        </button>
      </p>
      <p v-if="body.results.length && body.checkAt" class="m-0 text-caption text-ink-muted @md:hidden">
        {{ t('monitor.section.next_check') }} <time :datetime="body.checkAt">{{ intl.dateTime(body.checkAt) }}</time>
      </p>
    </template>
  </section>

  <JecDialog v-if="on && body?.tracked" v-model:open="editingPorts" :title="t('monitor.ports.title')">
    <form id="ports-form" class="flex flex-col gap-3" novalidate @submit.prevent="savePorts">
      <p class="m-0 text-small text-ink-muted">{{ t('monitor.ports.intro') }}</p>
      <JecAlert v-if="portsError.alert.value" :code="portsError.alert.value.code" :params="portsError.alert.value.params" />
      <JecField id="ports" label="monitor.ports.label" hint="monitor.ports.hint" :error="portsInvalid ? 'kernel.error.invalid_field' : undefined">
        <JecInput id="ports" v-model="portsText" inputmode="numeric" autocapitalize="none" spellcheck="false" :invalid="portsInvalid" translate="no" />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="editingPorts = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="ports-form" variant="primary" :busy="setPorts.isPending.value">{{ t('kernel.action.save') }}</JecButton>
    </template>
  </JecDialog>
</template>
