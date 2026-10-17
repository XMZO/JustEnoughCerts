<script setup lang="ts">
import { Circle, CircleCheck, CircleX, Download, LoaderCircle, OctagonX, RotateCw, TriangleAlert } from '@lucide/vue'
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { saveFile, useIssuance, useIssueNow, type LogLine } from '../api/certs'
import { useApiError } from '../api/errors'
import { useProviders } from '../api/providers'
import { stepStateText, stepText } from '../certs/issuance'
import { certLife } from '../certs/life'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import LifeBar from '../components/LifeBar.vue'
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

const is = computed(() => detail.data.value?.issuance)
const cert = computed(() => detail.data.value?.certificate)
const domain = computed(() => cert.value?.domains[0] ?? '')
const steps = computed(() => detail.data.value?.steps ?? [])
const lines = computed(() => detail.data.value?.log ?? [])
const life = computed(() => (cert.value ? certLife(cert.value) : null))

const running = computed(() => is.value?.state === 'running' || (is.value?.state === 'pending' && !is.value.error))
const retrying = computed(() => is.value?.state === 'pending' && !!is.value.error)

const via = computed(() => {
  const source = sources.data.value?.find((p) => p.id === cert.value?.sourceProvider)?.name ?? ''
  const dns = solvers.data.value?.find((p) => p.id === cert.value?.solverProvider)?.name
  return dns ? t('certs.issuance.via_dns', { source, dns }) : t('certs.issuance.via', { source })
})

const params = (p?: Record<string, unknown>) => (p ?? {}) as Params
const message = (l: { key: string; params?: Record<string, unknown> }) => t(l.key as MessageKey, params(l.params))

const stepIcons = { pending: Circle, running: LoaderCircle, done: CircleCheck, failed: OctagonX } as const
const stepInk = { pending: 'text-ink-muted', running: 'text-info-ink', done: 'text-ok-ink', failed: 'text-danger-ink' } as const
const progress = computed(() => {
  const at = steps.value.findIndex((s) => s.state === 'running' || s.state === 'failed')
  const current = steps.value[at]
  if (!current) return ''
  return t('certs.issuance.progress', { step: at + 1, steps: steps.value.length, name: t(stepText[current.step]) })
})
const levelInk = { info: 'text-ink', ok: 'text-ok-ink', warn: 'text-warn-ink', error: 'text-danger-ink' } as const

// The log follows new lines unless someone scrolled up to read.
const logBox = ref<HTMLElement>()
const follow = ref(true)
function onScroll() {
  const el = logBox.value
  if (el) follow.value = el.scrollHeight - el.scrollTop - el.clientHeight < 24
}
watch(
  () => lines.value.length,
  async () => {
    await nextTick()
    if (follow.value && logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
  },
)
const showDetail = ref(false)

/** The full log as text: every line with its time, attempt, level and raw detail. */
function downloadLog() {
  const text = lines.value
    .map((l: LogLine) => {
      const head = `${l.at}  #${l.attempt}  ${l.level.toUpperCase().padEnd(5)}  ${message(l)}`
      return l.detail ? `${head}\n    ${l.detail.replaceAll('\n', '\n    ')}` : head
    })
    .join('\n')
  saveFile(new Blob([`${text}\n`], { type: 'text/plain' }), `issuance-${props.issuance}.log`)
}

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
          <strong class="text-heading text-ok-ink">{{ t('certs.issuance.done') }}</strong>
          <span v-if="cert.current" class="text-small">
            {{ t('certs.issuance.done_until', { date: intl.date(cert.current.notAfter) }) }}
          </span>
        </div>
        <LifeBar v-if="life" :life="life" class="w-40 flex-none" />
      </section>
      <section v-else-if="retrying" role="status" class="flex gap-3 rounded-lg border border-line bg-warn-soft p-4">
        <TriangleAlert class="size-6 flex-none text-warn-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-col gap-0.5">
          <strong class="text-heading text-warn-ink">{{ t('certs.issuance.retrying', { attempt: is.attempts }) }}</strong>
          <span v-if="is.error" class="text-small">{{ message(is.error) }}</span>
          <span v-if="is.nextAt" class="text-small text-ink-muted">
            {{ t('certs.issuance.next_try') }} <time :datetime="is.nextAt">{{ intl.dateTime(is.nextAt) }}</time>
          </span>
        </div>
      </section>
      <section v-else-if="is.state === 'failed'" role="status" class="flex gap-3 rounded-lg border border-line bg-danger-soft p-4">
        <CircleX class="size-6 flex-none text-danger-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-col gap-0.5">
          <strong class="text-heading text-danger-ink">{{ t('certs.issuance.failed', { attempts: is.attempts }) }}</strong>
          <span v-if="is.error" class="text-small">{{ message(is.error) }}</span>
          <span class="text-small text-ink-muted">{{ t('certs.issuance.failed_hint') }}</span>
        </div>
      </section>

      <!-- Phones: the header's buttons. -->
      <div class="flex flex-col gap-2 sm:hidden">
        <JecButton v-if="is.state === 'failed' || retrying" variant="primary" :busy="issueNow.isPending.value" @click="retry">
          <RotateCw class="size-4" aria-hidden="true" />{{ t('certs.action.retry') }}
        </JecButton>
        <JecButton v-if="!running" :to="`/certificates/${id}`">{{ t('certs.action.details') }}</JecButton>
      </div>

      <div class="flex flex-wrap items-start gap-3 sm:gap-4">
        <section aria-labelledby="steps-title" class="flex min-w-0 flex-[1_1_260px] flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
          <h2 id="steps-title" class="m-0 text-section-title font-semibold">{{ t('certs.issuance.steps') }}</h2>
          <p class="m-0 text-small text-ink-muted" aria-live="polite">{{ is.state === 'pending' && !is.attempts ? t('certs.issuance.queued') : progress }}</p>
          <ol class="m-0 flex list-none flex-col gap-1 p-0" :aria-label="t('certs.issuance.steps')">
            <li v-for="s in steps" :key="s.step" class="grid grid-cols-[20px_minmax(0,1fr)_auto] items-center gap-2 py-1">
              <component
                :is="stepIcons[s.state]"
                :class="['size-5', stepInk[s.state], s.state === 'running' ? 'animate-spin' : '']"
                aria-hidden="true"
              />
              <span :class="['min-w-0', s.state === 'pending' ? 'text-ink-muted' : '', s.state === 'running' ? 'font-semibold' : '']">
                {{ t(stepText[s.step]) }}
                <span class="sr-only">{{ t(stepStateText[s.state]) }}</span>
              </span>
              <time v-if="s.at" :datetime="s.at" class="text-caption text-ink-muted tabular-nums">{{ intl.time(s.at) }}</time>
            </li>
          </ol>
        </section>

        <section aria-labelledby="log-title" class="flex min-w-0 flex-[999_1_480px] flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 id="log-title" class="m-0 text-section-title font-semibold">{{ t('certs.issuance.log') }}</h2>
            <div class="flex flex-wrap items-center gap-2">
              <label class="flex min-h-9 items-center gap-1.5 text-small">
                <input v-model="showDetail" type="checkbox" class="size-4 accent-brand" />{{ t('certs.issuance.show_detail') }}
              </label>
              <JecButton size="sm" :disabled="!lines.length" @click="downloadLog">
                <Download class="size-4" aria-hidden="true" />{{ t('certs.issuance.download_log') }}
              </JecButton>
            </div>
          </div>
          <div
            ref="logBox"
            role="log"
            aria-live="polite"
            :aria-label="t('certs.issuance.log')"
            class="max-h-[60dvh] min-h-24 overflow-y-auto rounded-md bg-surface-sunken p-3 text-small"
            @scroll="onScroll"
          >
            <p v-if="!lines.length" class="m-0 text-ink-muted">{{ t('certs.issuance.no_log') }}</p>
            <div v-for="l in lines" :key="l.seq" class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 py-0.5">
              <time :datetime="l.at" class="text-ink-muted tabular-nums">{{ intl.time(l.at) }}</time>
              <span :class="['[overflow-wrap:anywhere]', levelInk[l.level], l.level === 'info' ? '' : 'font-semibold']">{{ message(l) }}</span>
              <pre
                v-if="showDetail && l.detail"
                translate="no"
                class="col-start-2 m-0 font-mono text-caption whitespace-pre-wrap text-ink-muted [overflow-wrap:anywhere]"
              >{{ l.detail }}</pre>
            </div>
            <div v-if="running" class="py-0.5" aria-hidden="true"><span class="inline-block h-4 w-2 animate-pulse bg-ink-muted align-middle" /></div>
          </div>
        </section>
      </div>
    </template>
  </div>
</template>
