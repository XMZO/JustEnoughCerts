<script setup lang="ts">
import { CircleX, Download, Hourglass, LoaderCircle, Pencil, Plus, Send, ShieldCheck, Trash2, Upload } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import {
  saveFile,
  useCertificate,
  useDeleteCertificate,
  useDeployNow,
  useDownload,
  useIssuances,
  useIssueNow,
  useUpdateCertificate,
  useUploadVersion,
} from '../api/certs'
import { useApiError } from '../api/errors'
import { useProviders, useProviderTypes } from '../api/providers'
import { deploymentState, failingDeployment } from '../certs/deployment'
import { issuanceState, reasonText } from '../certs/issuance'
import { certLife, colonHex, keyText } from '../certs/life'
import CertSlot from '../components/CertSlot.vue'
import CopyButton from '../components/CopyButton.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import LifeBar from '../components/LifeBar.vue'
import PageHeader from '../components/PageHeader.vue'
import PemFields from '../components/PemFields.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'
import { typeName } from '../providers/fields'

// A certificate (docs/design/boards/CertDetail and CertDetailMobile): its
// validity, details, renewal settings, versions and chain, with the upload
// of a new version and deletion. One with a source also shows its latest
// issuances and can be issued again at once.
const props = defineProps<{ id: number }>()

const { t } = useT()
const intl = useIntl()
const route = useRoute()
const router = useRouter()
const detail = useCertificate(() => props.id)
const loadError = useApiError(detail.error)

const cert = computed(() => detail.data.value?.certificate ?? null)
const versions = computed(() => detail.data.value?.versions ?? [])
const chain = computed(() => detail.data.value?.chain ?? [])
const current = computed(() => cert.value?.current ?? null)
const life = computed(() => (cert.value ? certLife(cert.value) : null))
const domain = computed(() => cert.value?.domains[0] ?? '')
const also = computed(() => (cert.value && cert.value.domains.length > 1 ? intl.list(cert.value.domains.slice(1)) : ''))

const day = 86_400_000
const validityDays = computed(() =>
  current.value ? Math.round((Date.parse(current.value.notAfter) - Date.parse(current.value.notBefore)) / day) : 0,
)
const reminderAt = computed(() =>
  current.value && cert.value ? Date.parse(current.value.notAfter) - cert.value.renewBeforeDays * day : 0,
)
const key = computed(() => (current.value ? keyText(current.value.keyType) : null))

// Issuing, for a certificate with a source.
const issued = computed(() => cert.value?.source === 'provider')
const sources = useProviders('source')
const solvers = useProviders('solver')
const sourceName = computed(() => sources.data.value?.find((p) => p.id === cert.value?.sourceProvider)?.name)
const solverName = computed(() => solvers.data.value?.find((p) => p.id === cert.value?.solverProvider)?.name)
const issuances = useIssuances(() => props.id, issued)
const latest = computed(() => cert.value?.issuance)
const latestLink = computed(() => (latest.value ? `/certificates/${props.id}/issuances/${latest.value.id}` : ''))
const issueNow = useIssueNow(() => props.id)
const issueError = useApiError(issueNow.error)
async function startIssue() {
  let next
  try {
    next = await issueNow.mutateAsync(undefined)
  } catch {
    return // shown under the header
  }
  await router.push(`/certificates/${props.id}/issuances/${next.id}`)
}
const errorText = (e?: { key: string; params?: Record<string, unknown> }) => (e ? t(e.key as MessageKey, (e.params ?? {}) as Params) : '')

// Deploying, to the certificate's targets.
const targets = useProviders('target')
const targetTypes = useProviderTypes('target')
const targetLabel = (id: number) => {
  const p = targets.data.value?.find((x) => x.id === id)
  const n = typeName(targetTypes.data.value?.find((x) => x.id === p?.type))
  return { name: p?.name ?? '', type: n.key ? t(n.key) : (n.brand ?? '') }
}
const deployRows = computed(() =>
  (cert.value?.targets ?? []).map((target) => ({
    target,
    label: targetLabel(target),
    d: cert.value?.deployments.find((x) => x.target === target),
  })),
)
const deployInk = { pending: 'text-ink-muted', running: 'text-info-ink', done: 'text-ok-ink', failed: 'text-danger-ink', canceled: 'text-ink-muted' } as const
const failing = computed(() => (cert.value?.state === 'deploy_failed' ? failingDeployment(cert.value) : undefined))
const deployNow = useDeployNow(() => props.id)
const deployError = useApiError(deployNow.error)
async function deployAll() {
  try {
    await deployNow.mutateAsync(undefined)
  } catch {
    // shown in the section
  }
}

// Choosing the targets, in a dialog.
const choosing = ref(false)
const chosen = ref<number[]>([])
function chooseTargets() {
  chosen.value = [...(cert.value?.targets ?? [])]
  update.reset()
  choosing.value = true
}
async function saveTargets() {
  try {
    await update.mutateAsync({ targets: chosen.value })
  } catch {
    return // shown in the dialog
  }
  choosing.value = false
}

// A version was replaced when the next newer one came.
const history = computed(() =>
  versions.value.map((v, i) => ({
    version: v,
    range: t('certs.history.range', { from: intl.date(v.notBefore), to: intl.date(v.notAfter) }),
    note:
      v.id === current.value?.id
        ? t('certs.history.current')
        : Date.parse(v.notAfter) < Date.now()
          ? t('certs.state.expired')
          : t('certs.history.replaced', { date: intl.date(versions.value[i - 1]?.createdAt ?? v.createdAt) }),
  })),
)

// Uploading a new version, also opened by ?upload=1 from other pages.
const uploading = ref(route.query.upload === '1')
const upload = useUploadVersion(() => props.id)
const uploadError = useApiError(upload.error)
const pem = ref({ certificate: '', privateKey: '' })
watch(uploading, (open) => {
  if (!open) {
    upload.reset()
    pem.value = { certificate: '', privateKey: '' }
    if (route.query.upload) void router.replace({ query: {} })
  }
})
async function submitUpload() {
  try {
    await upload.mutateAsync({ certificate: pem.value.certificate, privateKey: pem.value.privateKey || undefined })
  } catch {
    return // shown in the dialog
  }
  uploading.value = false
}

// Renewal settings, edited in place.
const editing = ref(false)
const days = ref('')
const update = useUpdateCertificate(() => props.id)
const updateError = useApiError(update.error)
function edit() {
  days.value = String(cert.value?.renewBeforeDays ?? 30)
  update.reset()
  editing.value = true
}
async function saveSettings() {
  try {
    await update.mutateAsync({ renewBeforeDays: Number(days.value) })
  } catch {
    return
  }
  editing.value = false
}

// Downloading a version with its key, after the password once more.
const downloading = ref<number | null>(null)
const downloadOpen = computed({
  get: () => downloading.value !== null,
  set: (open) => {
    if (!open) downloading.value = null
  },
})
const password = ref('')
const download = useDownload(() => props.id)
const downloadError = useApiError(download.error)
function startDownload(version: number) {
  password.value = ''
  download.reset()
  downloading.value = version
}
async function submitDownload() {
  if (downloading.value === null) return
  try {
    const file = await download.mutateAsync({ version: downloading.value, password: password.value })
    saveFile(file.blob, file.name)
  } catch {
    return // shown in the dialog
  }
  downloading.value = null
}

const deleting = ref(false)
const remove = useDeleteCertificate(() => props.id)
const removeError = useApiError(remove.error)
async function confirmDelete() {
  try {
    await remove.mutateAsync(undefined)
  } catch {
    return
  }
  deleting.value = false
  await router.replace('/certificates')
}
</script>

<template>
  <PageHeader :title="domain || t('certs.detail.title')" :data="!!domain" :back="{ to: '/certificates', label: 'certs.list.title' }">
    <template #phone>
      <button
        v-if="cert"
        type="button"
        :aria-label="t('certs.action.upload')"
        class="grid size-11 cursor-pointer place-items-center rounded-md border-0 bg-brand text-on-brand hover:bg-brand-hover"
        @click="uploading = true"
      >
        <Upload class="size-4" aria-hidden="true" />
      </button>
    </template>
    <template #badge>
      <StatusBadge v-if="cert" :state="cert.state" />
    </template>
    <template #subtitle>
      <p v-if="also" class="m-0 text-ink-muted">{{ t('certs.detail.also', { names: also }) }}</p>
    </template>
    <template v-if="cert" #actions>
      <JecButton @click="deleting = true"><Trash2 class="size-4" aria-hidden="true" />{{ t('certs.action.delete') }}</JecButton>
      <JecButton v-if="current" @click="startDownload(current.id)">
        <Download class="size-4" aria-hidden="true" />{{ t('certs.action.download') }}
      </JecButton>
      <JecButton :variant="issued ? 'default' : 'primary'" @click="uploading = true">
        <Upload class="size-4" aria-hidden="true" />{{ t('certs.action.upload') }}
      </JecButton>
      <JecButton v-if="issued" variant="primary" :busy="issueNow.isPending.value" @click="startIssue">
        <ShieldCheck class="size-4" aria-hidden="true" />{{ t('certs.action.issue_now') }}
      </JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />
    <JecAlert v-if="issueError.error.value" :code="issueError.error.value.code" :params="issueError.error.value.params" />

    <template v-if="cert">
      <section
        v-if="latest && cert.state === 'issuing'"
        role="status"
        class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-line bg-info-soft px-4 py-3"
      >
        <Hourglass v-if="latest.state === 'waiting'" class="size-5 flex-none text-info-ink" aria-hidden="true" />
        <LoaderCircle v-else class="size-5 flex-none animate-spin text-info-ink" aria-hidden="true" />
        <span class="flex min-w-0 flex-1 flex-col gap-0.5">
          <span class="font-semibold text-info-ink">{{ t(latest.state === 'waiting' ? 'certs.detail.waiting' : 'certs.detail.issuing') }}</span>
          <span v-if="latest.state === 'waiting' && latest.nextAt" class="text-small text-ink-muted">
            {{ t('certs.issuance.next_check') }} <time :datetime="latest.nextAt">{{ intl.dateTime(latest.nextAt) }}</time>
          </span>
        </span>
        <RouterLink :to="latestLink" class="font-semibold">{{ t('certs.detail.show_progress') }}</RouterLink>
      </section>
      <section v-else-if="latest && cert.state === 'failed'" role="status" class="flex flex-wrap gap-x-3 gap-y-2 rounded-lg border border-line bg-danger-soft px-4 py-3">
        <CircleX class="mt-0.5 size-5 flex-none text-danger-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-[1_1_240px] flex-col gap-0.5">
          <strong class="text-danger-ink">{{ t(latest.state === 'failed' ? 'certs.detail.failed' : 'certs.detail.retrying') }}</strong>
          <span class="text-small [overflow-wrap:anywhere]">{{ errorText(latest.error) }}</span>
          <span v-if="latest.state === 'pending' && latest.nextAt" class="text-small text-ink-muted">
            {{ t('certs.issuance.next_try') }} <time :datetime="latest.nextAt">{{ intl.dateTime(latest.nextAt) }}</time>
          </span>
        </div>
        <RouterLink :to="latestLink" class="self-center font-semibold">{{ t('certs.detail.show_log') }}</RouterLink>
      </section>
      <section v-else-if="failing" role="status" class="flex flex-wrap gap-x-3 gap-y-2 rounded-lg border border-line bg-danger-soft px-4 py-3">
        <CircleX class="mt-0.5 size-5 flex-none text-danger-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-[1_1_240px] flex-col gap-0.5">
          <strong class="text-danger-ink [overflow-wrap:anywhere]">
            {{ t(failing.state === 'failed' ? 'certs.detail.deploy_failed' : 'certs.detail.deploy_retrying', { target: targetLabel(failing.target).name }) }}
          </strong>
          <span class="text-small [overflow-wrap:anywhere]">{{ errorText(failing.error) }}</span>
          <span v-if="failing.state === 'pending' && failing.nextAt" class="text-small text-ink-muted">
            {{ t('certs.issuance.next_try') }} <time :datetime="failing.nextAt">{{ intl.dateTime(failing.nextAt) }}</time>
          </span>
        </div>
        <RouterLink :to="`/certificates/${id}/deployments/${failing.id}`" class="self-center font-semibold">{{ t('certs.detail.show_log') }}</RouterLink>
      </section>
      <div class="flex items-center gap-3 sm:hidden">
        <StatusBadge :state="cert.state" />
        <p v-if="also" class="m-0 min-w-0 truncate text-small text-ink-muted" :title="t('certs.detail.also', { names: also })">
          {{ t('certs.detail.also', { names: also }) }}
        </p>
      </div>

      <section
        v-if="current"
        :aria-label="t('certs.detail.validity')"
        class="flex flex-wrap items-end gap-x-8 gap-y-4 rounded-lg border border-line bg-surface-raised p-4"
      >
        <CertSlot v-if="current" :cert="cert" decorative class="hidden sm:grid" />
        <LifeBar v-if="life" :life="life" large class="min-w-0 flex-[2_1_240px]" />
        <dl v-if="current" class="m-0 grid min-w-0 flex-[3_1_420px] grid-cols-2 gap-4 sm:grid-cols-4">
          <div>
            <dt class="text-caption text-ink-muted">{{ t('certs.field.not_before') }}</dt>
            <dd class="m-0 mt-0.5 font-semibold tabular-nums"><time :datetime="current.notBefore">{{ intl.date(current.notBefore) }}</time></dd>
          </div>
          <div>
            <dt class="text-caption text-ink-muted">{{ t('certs.field.not_after') }}</dt>
            <dd class="m-0 mt-0.5 font-semibold tabular-nums"><time :datetime="current.notAfter">{{ intl.date(current.notAfter) }}</time></dd>
          </div>
          <div>
            <dt class="text-caption text-ink-muted">{{ t('certs.field.validity') }}</dt>
            <dd class="m-0 mt-0.5 font-semibold tabular-nums">{{ t('certs.field.days', { days: validityDays }) }}</dd>
          </div>
          <div>
            <dt class="text-caption text-ink-muted">{{ t(issued ? 'certs.field.renewal' : 'certs.field.reminder') }}</dt>
            <dd class="m-0 mt-0.5 font-semibold tabular-nums">
              <time v-if="issued && cert.renewAt" :datetime="cert.renewAt">{{ intl.dateTime(cert.renewAt) }}</time>
              <time v-else :datetime="new Date(reminderAt).toISOString()">{{ intl.date(reminderAt) }}</time>
            </dd>
          </div>
        </dl>
      </section>

      <div class="flex flex-wrap items-start gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-[999_1_480px] flex-col gap-3 sm:gap-4">
          <section aria-labelledby="deploys" class="flex flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h2 id="deploys" class="m-0 text-section-title font-semibold">{{ t('certs.detail.deploys') }}</h2>
              <div class="flex flex-wrap gap-2">
                <JecButton v-if="cert.targets.length && current" size="sm" :busy="deployNow.isPending.value" @click="deployAll">
                  <Send class="size-4" aria-hidden="true" />{{ t('certs.action.deploy_now') }}
                </JecButton>
                <JecButton size="sm" variant="ghost" @click="chooseTargets">
                  <Pencil class="size-4" aria-hidden="true" />{{ t('certs.action.choose_targets') }}
                </JecButton>
              </div>
            </div>
            <JecAlert v-if="deployError.error.value" :code="deployError.error.value.code" :params="deployError.error.value.params" />
            <p v-if="!cert.targets.length" class="m-0 text-small text-ink-muted">{{ t('certs.detail.no_targets') }}</p>
            <ol v-else class="m-0 list-none p-0 text-small">
              <li v-for="r in deployRows" :key="r.target" class="border-b border-line last:border-b-0">
                <RouterLink
                  v-if="r.d"
                  :to="`/certificates/${id}/deployments/${r.d.id}`"
                  class="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-0.5 py-2 text-ink no-underline hover:text-ink"
                >
                  <span class="min-w-[8rem] flex-1 [overflow-wrap:anywhere]">
                    <span class="font-semibold" translate="no">{{ r.label.name }}</span>
                    <span class="ml-1.5 text-ink-muted" translate="no">{{ r.label.type }}</span>
                  </span>
                  <time v-if="r.d.finishedAt" :datetime="r.d.finishedAt" class="flex-none text-ink-muted tabular-nums">{{ intl.dateTime(r.d.finishedAt) }}</time>
                  <span :class="['flex-none font-semibold', deployInk[r.d.state]]">{{ t(deploymentState(r.d)) }}</span>
                </RouterLink>
                <div v-else class="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-0.5 py-2">
                  <span class="min-w-[8rem] flex-1 [overflow-wrap:anywhere]">
                    <span class="font-semibold" translate="no">{{ r.label.name }}</span>
                    <span class="ml-1.5 text-ink-muted" translate="no">{{ r.label.type }}</span>
                  </span>
                  <span class="flex-none text-ink-muted">{{ t('certs.deployment.state.none') }}</span>
                </div>
              </li>
            </ol>
          </section>

          <section v-if="issued" aria-labelledby="issuances" class="rounded-lg border border-line bg-surface-raised p-4">
            <h2 id="issuances" class="m-0 mb-1 text-section-title font-semibold">{{ t('certs.detail.issuances') }}</h2>
            <p v-if="issuances.isSuccess.value && !issuances.data.value?.length" class="m-0 text-small text-ink-muted">{{ t('certs.detail.no_issuances') }}</p>
            <ol class="m-0 list-none p-0 text-small">
              <li v-for="is in issuances.data.value ?? []" :key="is.id" class="border-b border-line last:border-b-0">
                <RouterLink
                  :to="`/certificates/${id}/issuances/${is.id}`"
                  class="flex min-h-11 flex-wrap items-center gap-x-3 gap-y-0.5 py-2 text-ink no-underline hover:text-ink"
                >
                  <time :datetime="is.createdAt" class="flex-none text-ink-muted tabular-nums">{{ intl.dateTime(is.createdAt) }}</time>
                  <span class="min-w-[8rem] flex-1 [overflow-wrap:anywhere]">{{ t(reasonText[is.reason]) }}</span>
                  <span :class="['flex-none font-semibold', is.state === 'done' ? 'text-ok-ink' : is.state === 'running' || is.state === 'waiting' || (is.state === 'pending' && !is.error) ? 'text-info-ink' : 'text-danger-ink']">
                    {{ t(issuanceState(is)) }}
                  </span>
                </RouterLink>
              </li>
            </ol>
          </section>

          <section v-if="versions.length" aria-labelledby="versions" class="rounded-lg border border-line bg-surface-raised p-4">
            <h2 id="versions" class="m-0 mb-1 text-section-title font-semibold">{{ t('certs.history.title') }}</h2>
            <ol class="m-0 list-none p-0 text-small">
              <li v-for="h in history" :key="h.version.id" class="flex items-center gap-2 border-b border-line py-2 last:border-b-0">
                <div class="min-w-0 flex-1">
                  <div class="tabular-nums">{{ h.range }}</div>
                  <div class="text-ink-muted">{{ h.note }}</div>
                </div>
                <button
                  type="button"
                  :aria-label="t('certs.action.download_version', { range: h.range })"
                  :title="t('certs.action.download_version', { range: h.range })"
                  class="grid size-11 flex-none cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink-muted hover:bg-surface-sunken hover:text-ink sm:size-8"
                  @click="startDownload(h.version.id)"
                >
                  <Download class="size-4" aria-hidden="true" />
                </button>
              </li>
            </ol>
          </section>

          <section v-if="chain.length" aria-labelledby="chain" class="rounded-lg border border-line bg-surface-raised p-4">
            <h2 id="chain" class="m-0 mb-1 text-section-title font-semibold">{{ t('certs.chain.title') }}</h2>
            <ol class="m-0 list-none p-0 text-small">
              <li v-for="(c, i) in chain" :key="i" class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-line py-2 last:border-b-0">
                <span class="min-w-0 flex-[1_1_200px] font-semibold [overflow-wrap:anywhere]" translate="no">{{ c.subject }}</span>
                <span class="min-w-0 flex-[1_1_200px] text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.chain.issued_by', { issuer: c.issuer }) }}</span>
                <span class="flex-none text-ink-muted tabular-nums">{{ t('certs.chain.expires', { date: intl.date(c.notAfter) }) }}</span>
              </li>
            </ol>
          </section>
        </div>

        <div class="flex min-w-0 flex-[1_1_320px] flex-col gap-3 sm:gap-4">
          <section aria-labelledby="info" class="rounded-lg border border-line bg-surface-raised p-4">
            <h2 id="info" class="m-0 mb-1 text-section-title font-semibold">{{ t('certs.info.title') }}</h2>
            <dl class="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-small">
              <dt class="whitespace-nowrap text-ink-muted">{{ t('certs.field.domains') }}</dt>
              <dd class="m-0 [overflow-wrap:anywhere]" translate="no">{{ intl.list(cert.domains) }}</dd>
              <template v-if="current">
                <dt class="whitespace-nowrap text-ink-muted">{{ t('certs.field.issuer') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]" translate="no">{{ current.issuer }}</dd>
              </template>
              <dt class="whitespace-nowrap text-ink-muted">{{ t('certs.field.source') }}</dt>
              <dd v-if="issued" class="m-0 [overflow-wrap:anywhere]" translate="no">{{ sourceName ?? '' }}</dd>
              <dd v-else class="m-0">{{ t('certs.source.upload') }}</dd>
              <template v-if="issued && cert.solverProvider">
                <dt class="whitespace-nowrap text-ink-muted">{{ t('certs.field.validation') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]" translate="no">{{ solverName ?? '' }}</dd>
              </template>
              <dt class="whitespace-nowrap text-ink-muted">{{ t('certs.field.key') }}</dt>
              <dd v-if="key" class="m-0">{{ t(key.key, key.params) }}</dd>
              <dd v-else class="m-0">{{ t(keyText(cert.keyType).key, keyText(cert.keyType).params) }}</dd>
              <template v-if="current">
                <dt class="whitespace-nowrap text-ink-muted">{{ t('certs.field.serial') }}</dt>
                <dd class="m-0 flex min-w-0 items-start gap-1">
                  <span class="min-w-0 flex-1 truncate font-mono text-caption leading-5" translate="no" :title="colonHex(current.serial)">
                    {{ colonHex(current.serial) }}
                  </span>
                  <CopyButton :value="colonHex(current.serial)" label="certs.action.copy_serial" />
                </dd>
                <dt class="whitespace-nowrap text-ink-muted">{{ t('certs.field.fingerprint') }}</dt>
                <dd class="m-0 flex min-w-0 items-start gap-1">
                  <span class="min-w-0 flex-1 truncate font-mono text-caption leading-5" translate="no" :title="colonHex(current.fingerprint)">
                    {{ colonHex(current.fingerprint) }}
                  </span>
                  <CopyButton :value="colonHex(current.fingerprint)" label="certs.action.copy_fingerprint" />
                </dd>
              </template>
            </dl>
          </section>

          <section aria-labelledby="settings" class="rounded-lg border border-line bg-surface-raised p-4">
            <div class="mb-1 flex items-center justify-between gap-3">
              <h2 id="settings" class="m-0 text-section-title font-semibold">{{ t('certs.settings.title') }}</h2>
              <JecButton v-if="!editing" variant="ghost" size="sm" @click="edit">
                <Pencil class="size-4" aria-hidden="true" />{{ t('kernel.action.edit') }}
              </JecButton>
            </div>
            <dl v-if="!editing" class="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-small">
              <dt class="whitespace-nowrap text-ink-muted">{{ t(issued ? 'certs.field.renewal' : 'certs.field.reminder') }}</dt>
              <dd class="m-0">{{ t('certs.settings.reminder_value', { days: cert.renewBeforeDays }) }}</dd>
            </dl>
            <form v-else class="flex flex-col gap-3" novalidate @submit.prevent="saveSettings">
              <JecField id="renewBeforeDays" label="certs.settings.days" hint="certs.settings.days_help" :error="updateError.fields.value.renewBeforeDays">
                <span class="flex items-center gap-2">
                  <span class="w-24 flex-none">
                    <JecInput
                      id="renewBeforeDays"
                      v-model="days"
                      type="number"
                      inputmode="numeric"
                      min="1"
                      max="365"
                      class="tabular-nums"
                      :invalid="!!updateError.fields.value.renewBeforeDays"
                    />
                  </span>
                  <span>{{ t('certs.settings.days_unit') }}</span>
                </span>
              </JecField>
              <JecAlert v-if="updateError.alert.value" :code="updateError.alert.value.code" :params="updateError.alert.value.params" />
              <div class="flex flex-wrap gap-2">
                <JecButton type="submit" variant="primary" size="sm" :busy="update.isPending.value">{{ t('kernel.action.save') }}</JecButton>
                <JecButton size="sm" @click="editing = false">{{ t('kernel.action.cancel') }}</JecButton>
              </div>
            </form>
          </section>

          <div class="flex flex-col gap-2 sm:hidden">
            <JecButton v-if="issued" variant="primary" :busy="issueNow.isPending.value" @click="startIssue">
              <ShieldCheck class="size-4" aria-hidden="true" />{{ t('certs.action.issue_now') }}
            </JecButton>
            <JecButton v-if="current" @click="startDownload(current.id)">
              <Download class="size-4" aria-hidden="true" />{{ t('certs.action.download') }}
            </JecButton>
            <JecButton @click="deleting = true"><Trash2 class="size-4" aria-hidden="true" />{{ t('certs.action.delete') }}</JecButton>
          </div>
        </div>
      </div>
    </template>
  </div>

  <JecDialog v-if="cert" v-model:open="uploading" :title="t('certs.upload.title')">
    <form id="upload-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitUpload">
      <p class="m-0 text-ink-muted">{{ t('certs.upload.intro', { domain }) }}</p>
      <JecAlert v-if="uploadError.alert.value" :code="uploadError.alert.value.code" :params="uploadError.alert.value.params" />
      <PemFields
        v-model:certificate="pem.certificate"
        v-model:private-key="pem.privateKey"
        :errors="uploadError.fields.value"
        :error-params="uploadError.error.value?.params"
      />
    </form>
    <template #footer>
      <JecButton @click="uploading = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="upload-form" variant="primary" :busy="upload.isPending.value">
        <Upload class="size-4" aria-hidden="true" />{{ t('certs.action.upload_short') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert" v-model:open="downloadOpen" :title="t('certs.download.title')" small>
    <form id="download-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitDownload">
      <p class="m-0">{{ t('certs.download.intro') }}</p>
      <p class="m-0 text-small text-ink-muted">{{ t('certs.download.files') }}</p>
      <JecAlert v-if="downloadError.alert.value" :code="downloadError.alert.value.code" :params="downloadError.alert.value.params" />
      <JecField id="download-password" label="kernel.account.confirm_password" :error="downloadError.fields.value.password">
        <JecInput
          id="download-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          :invalid="!!downloadError.fields.value.password"
          required
        />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="downloadOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="download-form" variant="primary" :busy="download.isPending.value" :disabled="!password">
        <Download class="size-4" aria-hidden="true" />{{ t('certs.action.download') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert" v-model:open="choosing" :title="t('certs.action.choose_targets')" small>
    <form id="targets-form" class="flex flex-col gap-3" novalidate @submit.prevent="saveTargets">
      <p class="m-0 text-ink-muted">{{ t('certs.targets.intro') }}</p>
      <JecAlert v-if="updateError.error.value" :code="updateError.error.value.code" :params="updateError.error.value.params" />
      <p v-if="targets.isSuccess.value && !targets.data.value?.length" class="m-0">{{ t('certs.add.no_targets') }}</p>
      <label v-for="p in targets.data.value ?? []" :key="p.id" class="flex min-h-11 items-center gap-2.5">
        <input v-model="chosen" type="checkbox" :value="p.id" class="size-4 flex-none accent-brand" />
        <span class="min-w-0 [overflow-wrap:anywhere]">
          <span class="font-semibold" translate="no">{{ p.name }}</span>
          <span class="ml-1.5 text-small text-ink-muted" translate="no">{{ targetLabel(p.id).type }}</span>
        </span>
      </label>
      <RouterLink to="/targets" class="flex items-center gap-1.5 self-start text-small"><Plus class="size-4" aria-hidden="true" />{{ t('certs.add.manage_targets') }}</RouterLink>
    </form>
    <template #footer>
      <JecButton @click="choosing = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="targets-form" variant="primary" :busy="update.isPending.value">{{ t('kernel.action.save') }}</JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert" v-model:open="deleting" :title="t('certs.delete.title', { domain })" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('certs.delete.body') }}</p>
      <JecAlert v-if="removeError.error.value" :code="removeError.error.value.code" :params="removeError.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="deleting = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="danger" :busy="remove.isPending.value" @click="confirmDelete">
        <Trash2 class="size-4" aria-hidden="true" />{{ t('certs.action.delete') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
