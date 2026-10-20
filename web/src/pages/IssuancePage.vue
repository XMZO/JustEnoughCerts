<script setup lang="ts">
import { CircleCheck, CircleX, Hourglass, RefreshCw, RotateCw, TriangleAlert } from '@lucide/vue'
import { computed } from 'vue'
import { useRouter } from 'vue-router'

import { useIssuance, useIssueNow } from '../api/certs'
import { useApiError } from '../api/errors'
import { useProviders } from '../api/providers'
import { deploySteps, stepText } from '../certs/issuance'
import { certLife } from '../certs/life'
import IssuanceSteps from '../components/IssuanceSteps.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import LifeBar from '../components/LifeBar.vue'
import LogView from '../components/LogView.vue'
import PageHeader from '../components/PageHeader.vue'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'

// The progress of an issuance (docs/design/boards/Issue and IssueMobile):
// the steps of its last attempt and its log, line by line, refreshed
// while it runs. The page can be left at any time: the issuance goes on.
const props = defineProps<{ id: number; issuance: number }>()

const { t } = useT()
const intl = useIntl()
const router = useRouter()
const detail = useIssuance(() => props.issuance)
const loadError = useApiError(detail.error)
const sources = useProviders('source')
const solvers = useProviders('solver')
const targets = useProviders('target')

const is = computed(() => detail.data.value?.issuance)
const cert = computed(() => detail.data.value?.certificate)
const domain = computed(() => cert.value?.domains[0] ?? '')
const steps = computed(() => detail.data.value?.steps ?? [])
const lines = computed(() => detail.data.value?.log ?? [])
const life = computed(() => (cert.value ? certLife(cert.value) : null))

const running = computed(() => is.value?.state === 'running' || is.value?.state === 'waiting' || (is.value?.state === 'pending' && !is.value.error))
const retrying = computed(() => is.value?.state === 'pending' && !!is.value.error)

const source = computed(() => sources.data.value?.find((p) => p.id === cert.value?.sourceProvider)?.name ?? '')
// The source with its backup, if any, which the attempts take turns with.
const sourceText = computed(() => {
  const backup = sources.data.value?.find((p) => p.id === cert.value?.backupProvider)?.name
  return backup ? t('certs.issuance.with_backup', { source: source.value, backup }) : source.value
})
const targetName = (id: number) => targets.data.value?.find((p) => p.id === id)?.name ?? ''
const via = computed(() => {
  const dns = solvers.data.value?.find((p) => p.id === cert.value?.solverProvider)?.name
  const to = intl.list((cert.value?.targets ?? []).map(targetName).filter(Boolean))
  if (to) return dns ? t('certs.issuance.via_dns_targets', { source: sourceText.value, dns, targets: to }) : t('certs.issuance.via_targets', { source: sourceText.value, targets: to })
  return dns ? t('certs.issuance.via_dns', { source: sourceText.value, dns }) : t('certs.issuance.via', { source: sourceText.value })
})

// The deployments of the version the issuance brings, for the banner.
const deploys = computed(() => (detail.data.value ? deploySteps(detail.data.value) : []))
const deployed = computed(() => deploys.value.filter((d) => d.state === 'done').length)

const params = (p?: Record<string, unknown>) => (p ?? {}) as Params
const message = (l: { key: string; params?: Record<string, unknown> }) => t(l.key as MessageKey, params(l.params))

const progress = computed(() => {
  const at = steps.value.findIndex((s) => s.state === 'running' || s.state === 'failed')
  const current = steps.value[at]
  if (!current) return ''
  return t('certs.issuance.progress', { step: at + 1, steps: steps.value.length, name: t(stepText[current.step]) })
})
const issueNow = useIssueNow(() => props.id)
const issueError = useApiError(issueNow.error)
async function retry() {
  let next
  try {
    next = await issueNow.mutateAsync(undefined)
  } catch {
    return // shown above the steps
  }
  if (next.id !== props.issuance) await router.replace(`/certificates/${props.id}/issuances/${next.id}`)
  else await detail.refetch()
}
</script>

<template>
  <PageHeader
    :title="domain ? t('certs.issuance.heading', { domain }) : t('certs.issuance.title')"
    :subtitle="cert ? via : undefined"
    :back="{ to: `/certificates/${id}`, label: 'certs.detail.title' }"
  >
    <template v-if="is" #actions>
      <JecButton v-if="running" to="/certificates">{{ t('certs.issuance.background') }}</JecButton>
      <JecButton v-if="is.state === 'failed' || retrying" variant="primary" :busy="issueNow.isPending.value" @click="retry">
        <RotateCw class="size-4" aria-hidden="true" />{{ t('certs.action.retry') }}
      </JecButton>
      <JecButton v-if="!running" :to="`/certificates/${id}`">{{ t('certs.action.details') }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p v-if="cert" class="m-0 text-ink-muted sm:hidden">{{ via }}</p>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />
    <JecAlert v-if="issueError.error.value" :code="issueError.error.value.code" :params="issueError.error.value.params" />

    <template v-if="is && cert">
      <section v-if="is.state === 'done'" role="status" class="flex flex-wrap items-center gap-x-6 gap-y-3 rounded-lg border border-line bg-ok-soft p-4">
        <CircleCheck class="size-6 flex-none text-ok-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-[1_1_280px] flex-col gap-0.5">
          <strong class="text-heading text-ok-ink">
            <template v-if="!deploys.length">{{ t('certs.issuance.done') }}</template>
            <template v-else-if="deployed === deploys.length">{{ t('certs.issuance.done_deployed', { count: deploys.length }) }}</template>
            <template v-else>{{ t('certs.issuance.done_deploying', { count: deploys.length }) }}</template>
          </strong>
          <span v-if="cert.current && cert.renewAt" class="text-small">
            {{ t('certs.issuance.done_renew', { date: intl.date(cert.current.notAfter), time: intl.dateTime(cert.renewAt) }) }}
          </span>
          <span v-else-if="cert.current" class="text-small">
            {{ t('certs.issuance.done_until', { date: intl.date(cert.current.notAfter) }) }}
          </span>
        </div>
        <LifeBar v-if="life" :life="life" class="w-40 flex-none" />
      </section>
      <section v-else-if="is.state === 'waiting'" role="status" class="flex flex-wrap items-center gap-3 rounded-lg border border-line bg-info-soft p-4">
        <Hourglass class="size-6 flex-none text-info-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-[1_1_240px] flex-col gap-0.5">
          <strong class="text-heading text-info-ink [overflow-wrap:anywhere]">{{ t('certs.issuance.waiting', { source }) }}</strong>
          <span v-if="is.nextAt" class="text-small text-ink-muted">
            {{ t('certs.issuance.next_check') }} <time :datetime="is.nextAt">{{ intl.dateTime(is.nextAt) }}</time>
          </span>
        </div>
        <!-- Once something was done at the source, such as in its panel, rather than waiting for the next look. -->
        <JecButton size="sm" class="flex-none" :busy="issueNow.isPending.value" @click="retry">
          <RefreshCw class="size-4" aria-hidden="true" />{{ t('certs.issuance.check_now') }}
        </JecButton>
      </section>
      <section v-else-if="retrying" role="status" class="flex gap-3 rounded-lg border border-line bg-warn-soft p-4">
        <TriangleAlert class="size-6 flex-none text-warn-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-col gap-0.5">
          <strong class="text-heading text-warn-ink">{{ t('certs.issuance.retrying', { attempt: is.attempts }) }}</strong>
          <span v-if="is.error" class="text-small [overflow-wrap:anywhere]">{{ message(is.error) }}</span>
          <span v-if="is.nextAt" class="text-small text-ink-muted">
            {{ t('certs.issuance.next_try') }} <time :datetime="is.nextAt">{{ intl.dateTime(is.nextAt) }}</time>
          </span>
        </div>
      </section>
      <section v-else-if="is.state === 'failed'" role="status" class="flex gap-3 rounded-lg border border-line bg-danger-soft p-4">
        <CircleX class="size-6 flex-none text-danger-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-col gap-0.5">
          <strong class="text-heading text-danger-ink">{{ t('certs.issuance.failed', { attempts: is.attempts }) }}</strong>
          <span v-if="is.error" class="text-small [overflow-wrap:anywhere]">{{ message(is.error) }}</span>
          <span class="text-small text-ink-muted">{{ t('certs.issuance.failed_hint') }}</span>
        </div>
      </section>

      <!-- Phones: the header's buttons. -->
      <div class="flex flex-col gap-2 sm:hidden">
        <JecButton v-if="is.state === 'failed' || retrying" variant="primary" :busy="issueNow.isPending.value" @click="retry">
          <RotateCw class="size-4" aria-hidden="true" />{{ t('certs.action.retry') }}
        </JecButton>
        <JecButton v-if="!running" :to="`/certificates/${id}`">{{ t('certs.action.details') }}</JecButton>
        <JecButton v-else to="/certificates">{{ t('certs.issuance.background') }}</JecButton>
      </div>

      <div class="flex flex-wrap items-start gap-3 sm:gap-4">
        <section aria-labelledby="steps-title" class="flex min-w-0 flex-[1_1_260px] flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
          <h2 id="steps-title" class="m-0 text-section-title font-semibold">{{ t('certs.issuance.steps') }}</h2>
          <p class="m-0 text-small text-ink-muted" aria-live="polite">{{ is.state === 'pending' && !is.attempts ? t('certs.issuance.queued') : progress }}</p>
          <IssuanceSteps v-if="detail.data.value" :detail="detail.data.value" />
        </section>

        <LogView :lines="lines" :running="running" :filename="`issuance-${issuance}.log`" class="flex-[999_1_480px]" />
      </div>
    </template>
  </div>
</template>
