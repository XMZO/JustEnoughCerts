<script setup lang="ts">
import { CircleStop } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { useCancelIssuance, type Certificate, type Issuance } from '../api/certs'
import { useApiError } from '../api/errors'
import { useProviders } from '../api/providers'
import { useT } from '../i18n'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'

/** StopAsk is an issuance to stop, with its certificate. */
export interface StopAsk {
  issuance: Pick<Issuance, 'id' | 'state'>
  certificate: Pick<Certificate, 'paused' | 'sourceProvider'>
}

// Asks before stopping an issuance (PLAN.md 4.3): what ends, that nobody
// is told of it as of a failure, when renewals start again, and that a
// source's order stays.
const ask = defineModel<StopAsk | undefined>('ask', { required: true })
const emit = defineEmits<{ stopped: [] }>()

const { t } = useT()
const sources = useProviders('source')
const cancel = useCancelIssuance()

// What it shows stays while it closes.
const shown = ref<StopAsk>()
const failure = ref<unknown>()
const failed = useApiError(failure)
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
const source = computed(() => sources.data.value?.find((p) => p.id === shown.value?.certificate.sourceProvider)?.name ?? '')

async function confirm() {
  const a = ask.value
  if (!a || cancel.isPending.value) return
  failure.value = undefined
  try {
    await cancel.mutateAsync(a.issuance.id)
  } catch (err) {
    failure.value = err
    return
  }
  if (ask.value === a) ask.value = undefined
  emit('stopped')
}
</script>

<template>
  <JecDialog v-model:open="open" :title="t('certs.stop.title')" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t(shown?.certificate.paused ? 'certs.stop.body_paused' : 'certs.stop.body') }}</p>
      <!-- A source that waits keeps an order, as a vendor's. -->
      <p v-if="shown?.issuance.state === 'waiting' && source" class="m-0 [overflow-wrap:anywhere]">{{ t('certs.stop.order', { source }) }}</p>
      <JecAlert v-if="failed.error.value" :code="failed.error.value.code" :params="failed.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="danger" :busy="cancel.isPending.value" @click="confirm">
        <CircleStop class="size-4" aria-hidden="true" />{{ t('certs.stop.confirm') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
