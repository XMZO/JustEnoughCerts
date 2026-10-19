<script setup lang="ts">
import { RefreshCw, Send } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import type { Certificate } from '../api/certs'
import { useApiError } from '../api/errors'
import { useProviders } from '../api/providers'
import { useT } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'

/**
 * CertAsk is something to do to certificates that work, asked first:
 * issue renews them now, which gets new ones from their sources (or, for
 * a source with keys of its own, collects, which looks there for a newer
 * one); deploy deploys the current version again, to target or to all the
 * targets.
 */
export interface CertAsk {
  kind: 'issue' | 'deploy'
  certs: Certificate[]
  collects?: boolean
  target?: number
  run: () => Promise<unknown>
}

// Asks before an action a menu item or a button makes easy to take by
// mistake: what it does and what stays as it is, then runs it, showing
// what went wrong in place.
const ask = defineModel<CertAsk | undefined>('ask', { required: true })

const { t } = useT()
const intl = useIntl()
const sources = useProviders('source')
const targets = useProviders('target')

// What it shows stays while it closes.
const shown = ref<CertAsk>()
const failure = ref<unknown>()
const failed = useApiError(failure)
const busy = ref(false)
watch(ask, (a) => {
  if (a) shown.value = a
  failure.value = undefined
})
const open = computed({
  get: () => !!ask.value,
  set: (now) => {
    if (!now) ask.value = undefined
  },
})

const one = computed(() => (shown.value?.certs.length === 1 ? shown.value.certs[0] : undefined))
const domain = computed(() => one.value?.domains[0] ?? '')
const sourceName = computed(() => sources.data.value?.find((p) => p.id === one.value?.sourceProvider)?.name ?? '')
const targetNames = computed(() => {
  const ids = shown.value?.target !== undefined ? [shown.value.target] : (one.value?.targets ?? [])
  return ids.map((id) => targets.data.value?.find((p) => p.id === id)?.name ?? '').filter(Boolean)
})
const expiry = computed(() => (one.value?.current ? intl.date(one.value.current.notAfter) : ''))
// When they renew by themselves, if later than now.
const scheduled = computed(() => {
  const at = (shown.value?.certs ?? []).map((c) => (c.renewAt ? Date.parse(c.renewAt) : Number.NaN)).filter((x) => x > Date.now())
  return at.length ? intl.dateTime(Math.min(...at)) : ''
})

const title = computed(() => {
  const a = shown.value
  if (!a) return ''
  if (a.kind === 'deploy') {
    return a.target !== undefined
      ? t('certs.confirm.redeploy_to_title', { target: targetNames.value[0] ?? '' })
      : t('certs.confirm.redeploy_title', { domain: domain.value })
  }
  if (a.certs.length > 1) return t('certs.confirm.renew_all_title', { count: a.certs.length })
  return t(a.collects ? 'certs.confirm.collect_title' : 'certs.confirm.renew_title', { domain: domain.value })
})

const lines = computed(() => {
  const a = shown.value
  if (!a) return []
  const targetsText = intl.list(targetNames.value)
  if (a.kind === 'deploy') return [t('certs.confirm.redeploy', { expiry: expiry.value, targets: targetsText })]
  if (a.certs.length > 1) return [t('certs.confirm.renew_all', { time: scheduled.value || intl.dateTime(Date.now()) })]
  const out = [t(a.collects ? 'certs.confirm.collect' : 'certs.confirm.renew', { source: sourceName.value })]
  if (targetNames.value.length) out.push(t('certs.confirm.then_deploy', { targets: targetsText }))
  if (expiry.value && scheduled.value) {
    out.push(t(a.collects ? 'certs.confirm.collect_schedule' : 'certs.confirm.renew_schedule', { expiry: expiry.value, date: scheduled.value }))
  }
  return out
})

const confirmLabel = computed(() => {
  const a = shown.value
  if (a?.kind === 'deploy') return t('certs.action.redeploy')
  return t(a?.collects && a.certs.length === 1 ? 'certs.action.collect_now' : 'certs.action.renew_now')
})

async function confirm() {
  const a = ask.value
  if (!a || busy.value) return
  busy.value = true
  failure.value = undefined
  try {
    await a.run()
  } catch (err) {
    failure.value = err
    return
  } finally {
    busy.value = false
  }
  if (ask.value === a) ask.value = undefined
}
</script>

<template>
  <JecDialog v-model:open="open" :title="title" small>
    <div class="flex flex-col gap-3">
      <p v-for="line in lines" :key="line" class="m-0">{{ line }}</p>
      <ul v-if="(shown?.certs.length ?? 0) > 1" class="m-0 flex flex-col gap-1 pl-5">
        <li v-for="c in shown?.certs" :key="c.id" translate="no">{{ c.domains[0] }}</li>
      </ul>
      <JecAlert v-if="failed.error.value" :code="failed.error.value.code" :params="failed.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="primary" :busy="busy" @click="confirm">
        <component :is="shown?.kind === 'deploy' ? Send : RefreshCw" class="size-4" aria-hidden="true" />{{ confirmLabel }}
      </JecButton>
    </template>
  </JecDialog>
</template>
