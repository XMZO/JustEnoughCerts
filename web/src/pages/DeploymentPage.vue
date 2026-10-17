<script setup lang="ts">
import { CircleCheck, CircleSlash, CircleX, LoaderCircle, RotateCw, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'

import { useDeployment, useDeployNow } from '../api/certs'
import { useApiError } from '../api/errors'
import { useProviders } from '../api/providers'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import LogView from '../components/LogView.vue'
import PageHeader from '../components/PageHeader.vue'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'

// A deployment of a version to a target, with its log, refreshed while it
// runs; one that failed can be tried again from here.
const props = defineProps<{ id: number; deployment: number }>()

const { t } = useT()
const intl = useIntl()
const detail = useDeployment(() => props.deployment)
const loadError = useApiError(detail.error)
const targets = useProviders('target')

const d = computed(() => detail.data.value?.deployment)
const cert = computed(() => detail.data.value?.certificate)
const lines = computed(() => detail.data.value?.log ?? [])
const target = computed(() => targets.data.value?.find((p) => p.id === d.value?.target)?.name ?? '')
const domain = computed(() => cert.value?.domains[0] ?? '')

const running = computed(() => d.value?.state === 'running' || (d.value?.state === 'pending' && !d.value.error))
const retrying = computed(() => d.value?.state === 'pending' && !!d.value.error)
const errorText = computed(() => (d.value?.error ? t(d.value.error.key as MessageKey, (d.value.error.params ?? {}) as Params) : ''))

const deployNow = useDeployNow(() => props.id)
const deployError = useApiError(deployNow.error)
async function retry() {
  try {
    await deployNow.mutateAsync(d.value?.target)
  } catch {
    return // shown above
  }
  await detail.refetch()
}
</script>

<template>
  <PageHeader
    :title="target ? t('certs.deployment.heading', { target }) : t('certs.deployment.title')"
    :back="{ to: `/certificates/${id}`, label: 'certs.detail.title' }"
  >
    <template #subtitle>
      <p v-if="domain" class="m-0 text-ink-muted" translate="no">{{ domain }}</p>
    </template>
    <template v-if="d" #actions>
      <JecButton v-if="d.state === 'failed' || retrying" variant="primary" :busy="deployNow.isPending.value" @click="retry">
        <RotateCw class="size-4" aria-hidden="true" />{{ t('certs.action.retry') }}
      </JecButton>
      <JecButton :to="`/certificates/${id}`">{{ t('certs.action.details') }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p v-if="domain" class="m-0 text-ink-muted sm:hidden" translate="no">{{ domain }}</p>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />
    <JecAlert v-if="deployError.error.value" :code="deployError.error.value.code" :params="deployError.error.value.params" />

    <template v-if="d">
      <section v-if="d.state === 'done'" role="status" class="flex gap-3 rounded-lg border border-line bg-ok-soft p-4">
        <CircleCheck class="size-6 flex-none text-ok-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-col gap-0.5">
          <strong class="text-heading text-ok-ink">{{ t('certs.deployment.done') }}</strong>
          <span v-if="d.finishedAt" class="text-small"><time :datetime="d.finishedAt">{{ intl.dateTime(d.finishedAt) }}</time></span>
        </div>
      </section>
      <section v-else-if="running" role="status" class="flex items-center gap-3 rounded-lg border border-line bg-info-soft p-4">
        <LoaderCircle class="size-6 flex-none animate-spin text-info-ink" aria-hidden="true" />
        <strong class="text-heading text-info-ink">{{ t('certs.deployment.running') }}</strong>
      </section>
      <section v-else-if="retrying" role="status" class="flex gap-3 rounded-lg border border-line bg-warn-soft p-4">
        <TriangleAlert class="size-6 flex-none text-warn-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-col gap-0.5">
          <strong class="text-heading text-warn-ink">{{ t('certs.issuance.retrying', { attempt: d.attempts }) }}</strong>
          <span class="text-small">{{ errorText }}</span>
          <span v-if="d.nextAt" class="text-small text-ink-muted">
            {{ t('certs.issuance.next_try') }} <time :datetime="d.nextAt">{{ intl.dateTime(d.nextAt) }}</time>
          </span>
        </div>
      </section>
      <section v-else-if="d.state === 'failed'" role="status" class="flex gap-3 rounded-lg border border-line bg-danger-soft p-4">
        <CircleX class="size-6 flex-none text-danger-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-col gap-0.5">
          <strong class="text-heading text-danger-ink">{{ t('certs.deployment.failed', { attempts: d.attempts }) }}</strong>
          <span class="text-small">{{ errorText }}</span>
          <span class="text-small text-ink-muted">{{ t('certs.issuance.failed_hint') }}</span>
        </div>
      </section>
      <section v-else role="status" class="flex items-center gap-3 rounded-lg border border-line bg-surface-raised p-4">
        <CircleSlash class="size-6 flex-none text-ink-muted" aria-hidden="true" />
        <span>{{ t('certs.deployment.canceled') }}</span>
      </section>

      <div v-if="d.state === 'failed' || retrying" class="flex flex-col gap-2 sm:hidden">
        <JecButton variant="primary" :busy="deployNow.isPending.value" @click="retry">
          <RotateCw class="size-4" aria-hidden="true" />{{ t('certs.action.retry') }}
        </JecButton>
      </div>

      <dl v-if="d.ref" class="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 rounded-lg border border-line bg-surface-raised p-4 text-small">
        <dt class="text-ink-muted">{{ t('certs.deployment.ref') }}</dt>
        <dd class="m-0 font-mono [overflow-wrap:anywhere]" translate="no">{{ d.ref }}</dd>
      </dl>

      <LogView :lines="lines" :running="running" :filename="`deployment-${deployment}.log`" />
    </template>
  </div>
</template>
