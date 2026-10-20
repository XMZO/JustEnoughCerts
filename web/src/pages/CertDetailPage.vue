<script setup lang="ts">
import {
  CircleX,
  Download,
  Hourglass,
  LoaderCircle,
  Pencil,
  Plus,
  RotateCw,
  Send,
  ShieldCheck,
  ShieldX,
  Trash2,
  Undo2,
  Upload,
} from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import {
  saveFile,
  useCertificate,
  useCertSettings,
  useCsr,
  useDeleteCertificate,
  useDeployNow,
  useDownload,
  useIssuance,
  useIssuances,
  useIssueNow,
  useRevoke,
  useUseVersion,
  useSetDomains,
  useUpdateCertificate,
  useUploadVersion,
  type RevokeReason,
} from '../api/certs'
import { useApiError } from '../api/errors'
import { useProviders, useProviderTypes, useProviderTypesOf } from '../api/providers'
import { domainNeedsLook, useDomains, useWatching } from '../api/watch'
import { deploymentState, failingDeployment } from '../certs/deployment'
import { deploySteps, issuanceState, reasonText } from '../certs/issuance'
import { caReasonText, certLife, colonHex, keyText, windowStart } from '../certs/life'
import { useDurationText } from '../certs/runs'
import CertActionDialog, { type CertAsk } from '../components/CertActionDialog.vue'
import CertSlot from '../components/CertSlot.vue'
import CopyButton from '../components/CopyButton.vue'
import DomainInput from '../components/DomainInput.vue'
import IssuanceSteps from '../components/IssuanceSteps.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import JecTextarea from '../components/JecTextarea.vue'
import LifeBar from '../components/LifeBar.vue'
import MonitorSection from '../components/MonitorSection.vue'
import PageHeader from '../components/PageHeader.vue'
import PemFields from '../components/PemFields.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'
import { summary, typeName } from '../providers/fields'

// A certificate (docs/design/boards/CertDetail and CertDetailMobile, made
// denser at the user's request so that it fits a screen): its validity,
// then its deployments, online checks and issuances beside its details,
// chain, renewal settings and versions, with the upload of a new version
// and deletion. One with a source also shows its last run and can be
// issued again at once.
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
// The registrations of the domains it is under, while the watch module is on.
const watching = useWatching()
const domainList = useDomains(watching)
const registrations = computed(() => (watching.value ? (domainList.data.value ?? []).filter((d) => d.certificates.includes(props.id)) : []))

const day = 86_400_000
const validityDays = computed(() =>
  current.value ? Math.round((Date.parse(current.value.notAfter) - Date.parse(current.value.notBefore)) / day) : 0,
)
const reminderAt = computed(() =>
  current.value && cert.value ? windowStart(current.value, cert.value.renewBeforeDays) : 0,
)
const key = computed(() => (current.value ? keyText(current.value.keyType) : null))

// What the current version says beyond its names and dates.
const details = computed(() => detail.data.value?.details)
type Details = NonNullable<typeof details.value>
const validationText: Record<NonNullable<Details['validation']>, MessageKey> = {
  dv: 'certs.validation.dv',
  ov: 'certs.validation.ov',
  iv: 'certs.validation.iv',
  ev: 'certs.validation.ev',
}
const ekuText: Record<Details['extKeyUsage'][number], MessageKey> = {
  any: 'certs.eku.any',
  server_auth: 'certs.eku.server_auth',
  client_auth: 'certs.eku.client_auth',
  code_signing: 'certs.eku.code_signing',
  email_protection: 'certs.eku.email_protection',
  time_stamping: 'certs.eku.time_stamping',
  ocsp_signing: 'certs.eku.ocsp_signing',
}
// Where the revocation of the version is looked up, and its issuer fetched.
const linkLabels = { ocsp: 'certs.revocation.ocsp', crl: 'certs.revocation.crl', ca: 'certs.revocation.ca' } as const satisfies Record<string, MessageKey>
const revocationLinks = computed(() => {
  const d = details.value
  if (!d) return []
  return [
    ...d.ocsp.map((href) => ({ href, label: linkLabels.ocsp })),
    ...d.crl.map((href) => ({ href, label: linkLabels.crl })),
    ...d.caIssuers.map((href) => ({ href, label: linkLabels.ca })),
  ]
})
// The fingerprints, each with a button to copy it.
const hashes = computed(() => {
  const c = current.value
  if (!c) return []
  const out: { label: MessageKey; value: string; copy: MessageKey }[] = [
    { label: 'certs.field.serial', value: colonHex(c.serial), copy: 'certs.action.copy_serial' },
    { label: 'certs.field.fingerprint', value: colonHex(c.fingerprint), copy: 'certs.action.copy_fingerprint' },
  ]
  if (details.value) {
    out.push(
      { label: 'certs.field.sha1', value: colonHex(details.value.sha1), copy: 'certs.action.copy_sha1' },
      { label: 'certs.field.spki', value: details.value.spkiSha256, copy: 'certs.action.copy_spki' },
    )
  }
  return out
})

// A request of the version's key for its names, made when asked for.
const showingCsr = ref(false)
const csr = useCsr(() => props.id, () => current.value?.id ?? 0, showingCsr)
const csrError = useApiError(csr.error)
function downloadCsr() {
  const text = csr.data.value?.csr
  if (text) saveFile(new Blob([text], { type: 'application/pkcs10' }), `${domain.value.replace('*', 'star')}.csr`)
}

// Issuing, for a certificate with a source.
const issued = computed(() => cert.value?.source === 'provider')
const sources = useProviders('source')
const solvers = useProviders('solver')
const sourceName = computed(() => sources.data.value?.find((p) => p.id === cert.value?.sourceProvider)?.name)
// A source with keys of its own is not asked to issue: it is looked at.
const sourceTypes = useProviderTypes('source')
const sourceType = computed(() => {
  const type = sources.data.value?.find((p) => p.id === cert.value?.sourceProvider)?.type
  return sourceTypes.data.value?.find((s) => s.id === type)
})
const ownKey = computed(() => !!sourceType.value?.ownKey)
const solverName = computed(() => solvers.data.value?.find((p) => p.id === cert.value?.solverProvider)?.name)
const issuances = useIssuances(() => props.id, issued)

const latest = computed(() => cert.value?.issuance)
const latestLink = computed(() => (latest.value ? `/certificates/${props.id}/issuances/${latest.value.id}` : ''))

// The last run (docs/design/boards/CertDetail): the latest issuance, its
// steps and the deployments of what it brought, and how it went.
const lastRun = useIssuance(() => latest.value?.id ?? 0, () => !!latest.value)
// The issuances before it, which the list shows under it.
const earlier = computed(() => (issuances.data.value ?? []).filter((is) => is.id !== lastRun.data.value?.issuance.id))
const durationText = useDurationText()
const runSummary = computed(() => {
  const d = lastRun.data.value
  const is = d?.issuance
  if (!d || !is?.startedAt) return ''
  const start = intl.dateTime(is.startedAt)
  const duration = is.finishedAt ? durationText(Math.round((Date.parse(is.finishedAt) - Date.parse(is.startedAt)) / 1000)) : ''
  if (is.state === 'done') {
    const deploys = deploySteps(d)
    if (deploys.some((x) => x.state === 'failed')) return t('certs.detail.run_deploy_failed', { start, duration })
    if (deploys.some((x) => x.state !== 'done')) return t('certs.detail.run_deploying', { start, duration })
    return t('certs.detail.run_done', { start, duration })
  }
  // A retry has no end yet; nor has an attempt under way.
  if (is.state === 'failed') return t('certs.detail.run_failed', { start, duration })
  if (is.state === 'pending' && is.error) return t('certs.detail.run_retrying', { start })
  return t('certs.detail.run_running', { start })
})
const issueNow = useIssueNow(() => props.id)
const issueError = useApiError(issueNow.error)
// Renewing a certificate that works, and deploying it again where it
// went well, ask first.
const asking = ref<CertAsk>()
const renews = computed(() => !!current.value && cert.value?.state !== 'failed' && cert.value?.state !== 'issuing')
const issueLabel = computed(() => (ownKey.value ? 'certs.action.collect_now' : current.value ? 'certs.action.renew_now' : 'certs.action.issue_now'))
async function issueAndShow() {
  const next = await issueNow.mutateAsync(undefined)
  await router.push(`/certificates/${props.id}/issuances/${next.id}`)
}
function startIssue() {
  if (renews.value && cert.value) asking.value = { kind: 'issue', certs: [cert.value], collects: ownKey.value, run: issueAndShow }
  else issueAndShow().catch(() => {}) // shown under the header
}
const errorText = (e?: { key: string; params?: Record<string, unknown> }) => (e ? t(e.key as MessageKey, (e.params ?? {}) as Params) : '')

// Deploying, to the certificate's targets.
const targets = useProviders('target')
// What the settings show: when renewals look, and who hears of them.
const certSettings = useCertSettings()
const notifiers = useProviders('notifier')
const noteEvents = ['on_issue_failed', 'on_deploy_failed', 'on_expiring', 'on_issued', 'on_deployed']
// The channels its notes go to: those it names, or else all, each that
// takes some of its events.
const notifying = computed(() =>
  (notifiers.data.value ?? [])
    .filter((n) => !cert.value?.notifyTo.length || cert.value.notifyTo.includes(n.id))
    .filter((n) => noteEvents.some((e) => n.config[e] === 'true'))
    .map((n) => n.name),
)
const targetTypes = useProviderTypes('target')
// Each target with its type, and the settings its card shows, as where
// it deploys to.
const targetOf = (id: number) => targets.data.value?.find((x) => x.id === id)
const targetType = useProviderTypesOf(() => [...new Set((cert.value?.targets ?? []).map((id) => targetOf(id)?.type ?? ''))].filter(Boolean))
const targetLabel = (id: number) => {
  const p = targetOf(id)
  const n = typeName(targetTypes.data.value?.find((x) => x.id === p?.type))
  return { name: p?.name ?? '', type: n.key ? t(n.key) : (n.brand ?? ''), brand: !n.key }
}
const deployRows = computed(() =>
  (cert.value?.targets ?? []).map((target) => {
    const p = targetOf(target)
    return {
      target,
      label: targetLabel(target),
      shown: p ? summary(targetType(p.type), p) : [],
      d: cert.value?.deployments.find((x) => x.target === target),
    }
  }),
)
const deployInk = { pending: 'text-ink-muted', running: 'text-info-ink', done: 'text-ok-ink', failed: 'text-danger-ink', canceled: 'text-ink-muted' } as const
const failing = computed(() => (cert.value?.state === 'deploy_failed' ? failingDeployment(cert.value) : undefined))
const deployNow = useDeployNow(() => props.id)
const deployError = useApiError(deployNow.error)
// The target being deployed to again, or 0 for all of them.
const deploying = ref<number>()
async function deployTo(target?: number) {
  deploying.value = target ?? 0
  try {
    await deployNow.mutateAsync(target)
  } catch {
    // shown in the section
  } finally {
    deploying.value = undefined
  }
}
const deployAll = () => deployTo()
/** startDeployAll deploys to every target, asking first when each went well. */
function startDeployAll() {
  const c = cert.value
  if (c && deployRows.value.every((r) => r.d?.state === 'done')) asking.value = { kind: 'deploy', certs: [c], run: () => deployNow.mutateAsync(undefined) }
  else void deployAll()
}
/** redeployTo deploys to target again, asking first where the last deployment there went well. */
function redeployTo(target: number, last?: string) {
  if (last === 'done' && cert.value) asking.value = { kind: 'deploy', certs: [cert.value], target, run: () => deployNow.mutateAsync(target) }
  else void deployTo(target)
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
const pem = ref({ certificate: '', privateKey: '', pfx: '', pfxPassword: '' })
watch(uploading, (open) => {
  if (!open) {
    upload.reset()
    pem.value = { certificate: '', privateKey: '', pfx: '', pfxPassword: '' }
    if (route.query.upload) void router.replace({ query: {} })
  }
})
async function submitUpload() {
  try {
    await upload.mutateAsync({
      certificate: pem.value.certificate || undefined,
      privateKey: pem.value.privateKey || undefined,
      pfx: pem.value.pfx || undefined,
      pfxPassword: pem.value.pfxPassword || undefined,
    })
  } catch {
    return // shown in the dialog
  }
  uploading.value = false
}

// Renewal settings, in the validity bar, edited in a dialog.
const rule = computed(() =>
  issued.value && certSettings.data.value
    ? t('certs.settings.renewal_value', { days: cert.value?.renewBeforeDays ?? 0, time: certSettings.data.value.checkTime })
    : t('certs.settings.reminder_value', { days: cert.value?.renewBeforeDays ?? 0 }),
)
const editing = ref(false)
const days = ref('')
// Keeping the private key across renewals, for a source whose keys JEC
// makes.
const reuseKey = ref(false)
const keyKept = computed(() => issued.value && !ownKey.value)
// The channels its notes go to; none chosen goes to all.
const notifyTo = ref<number[]>([])
const update = useUpdateCertificate(() => props.id)
const updateError = useApiError(update.error)
function edit() {
  days.value = String(cert.value?.renewBeforeDays ?? 30)
  reuseKey.value = !!cert.value?.reuseKey
  notifyTo.value = [...(cert.value?.notifyTo ?? [])]
  update.reset()
  editing.value = true
}
// The note: what people write about the certificate.
const noting = ref(false)
const note = ref('')
function editNote() {
  note.value = cert.value?.note ?? ''
  update.reset()
  noting.value = true
}
async function saveNote() {
  try {
    await update.mutateAsync({ note: note.value })
  } catch {
    return
  }
  noting.value = false
}

// The domains of a certificate with a source: changed, it is issued again
// at once for them.
const renaming = ref(false)
const newDomains = ref<string[]>([])
const setDomains = useSetDomains(() => props.id)
const domainsError = useApiError(setDomains.error)
const badNewDomains = computed(() =>
  Object.keys(domainsError.fields.value)
    .map((f) => /^domains\[(\d+)\]$/.exec(f)?.[1])
    .filter((i): i is string => i !== undefined)
    .map(Number),
)
function editDomains() {
  newDomains.value = [...(cert.value?.domains ?? [])]
  setDomains.reset()
  renaming.value = true
}
async function saveDomains() {
  try {
    await setDomains.mutateAsync(newDomains.value)
  } catch {
    return // shown in the dialog
  }
  renaming.value = false
}

async function saveSettings() {
  try {
    await update.mutateAsync({ renewBeforeDays: Number(days.value), notifyTo: notifyTo.value, ...(keyKept.value ? { reuseKey: reuseKey.value } : {}) })
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
// A PKCS #12 file too, for Windows, IIS and Java, with a password of its own.
const pfx = ref({ on: false, password: '', legacy: false })
const download = useDownload(() => props.id)
const downloadError = useApiError(download.error)
function startDownload(version: number) {
  password.value = ''
  pfx.value = { on: false, password: '', legacy: false }
  download.reset()
  downloading.value = version
}
async function submitDownload() {
  if (downloading.value === null) return
  try {
    const file = await download.mutateAsync({
      version: downloading.value,
      password: password.value,
      ...(pfx.value.on ? { pfx: true, pfxPassword: pfx.value.password, pfxLegacy: pfx.value.legacy } : {}),
    })
    saveFile(file.blob, file.name)
  } catch {
    return // shown in the dialog
  }
  downloading.value = null
}

// Revoking a version that a newer one replaced, after the password once
// more. The one in use is replaced first: the history says so.
const reasons: { value: RevokeReason; label: MessageKey }[] = [
  { value: 'superseded', label: 'certs.revoke.reason_superseded' },
  { value: 'key_compromise', label: 'certs.revoke.reason_key_compromise' },
  { value: 'cessation_of_operation', label: 'certs.revoke.reason_cessation_of_operation' },
  { value: 'unspecified', label: 'certs.revoke.reason_unspecified' },
]
const reasonLabel = (reason?: string): MessageKey => reasons.find((r) => r.value === reason)?.label ?? 'certs.revoke.reason_unspecified'
const revoking = ref<{ id: number; range: string } | null>(null)
const revokeOpen = computed({
  get: () => revoking.value !== null,
  set: (open) => {
    if (!open) revoking.value = null
  },
})
const revokeReason = ref<RevokeReason>('superseded')
const revokePassword = ref('')
const revoke = useRevoke(() => props.id)
const revokeError = useApiError(revoke.error)
function startRevoke(version: number, range: string) {
  revokePassword.value = ''
  revokeReason.value = 'superseded'
  revoke.reset()
  revoking.value = { id: version, range }
}
async function submitRevoke() {
  if (!revoking.value) return
  try {
    await revoke.mutateAsync({ version: revoking.value.id, password: revokePassword.value, reason: revokeReason.value })
  } catch {
    return // shown in the dialog
  }
  revoking.value = null
}

// Going back to an earlier version, once confirmed: it is deployed to every
// target, and the renewal goes by it.
const reverting = ref<{ id: number; range: string; notAfter: string } | null>(null)
const revertOpen = computed({
  get: () => reverting.value !== null,
  set: (open) => {
    if (!open) reverting.value = null
  },
})
const useVersion = useUseVersion(() => props.id)
const useVersionError = useApiError(useVersion.error)
function startRevert(version: number, range: string, notAfter: string) {
  useVersion.reset()
  reverting.value = { id: version, range, notAfter }
}
async function submitRevert() {
  if (!reverting.value) return
  try {
    await useVersion.mutateAsync(reverting.value.id)
  } catch {
    return // shown in the dialog
  }
  reverting.value = null
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
        <ShieldCheck class="size-4" aria-hidden="true" />{{ t(issueLabel) }}
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
        <RouterLink :to="latestLink" class="self-center font-semibold text-danger-ink underline underline-offset-2">{{ t('certs.detail.show_log') }}</RouterLink>
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
        <RouterLink :to="`/certificates/${id}/deployments/${failing.id}`" class="self-center font-semibold text-danger-ink underline underline-offset-2">{{ t('certs.detail.show_log') }}</RouterLink>
      </section>
      <section v-else-if="current?.caRevokedAt" role="status" class="flex flex-wrap gap-x-3 gap-y-2 rounded-lg border border-line bg-danger-soft px-4 py-3">
        <ShieldX class="mt-0.5 size-5 flex-none text-danger-ink" aria-hidden="true" />
        <div class="flex min-w-0 flex-[1_1_240px] flex-col gap-0.5">
          <strong class="text-danger-ink [overflow-wrap:anywhere]">
            {{ t('certs.history.ca_revoked', { date: intl.date(current.caRevokedAt), reason: t(caReasonText(current.caRevokeReason)) }) }}
          </strong>
          <span class="text-small [overflow-wrap:anywhere]">{{ t(issued ? 'certs.detail.revoked_renewing' : 'certs.detail.revoked_upload') }}</span>
        </div>
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
        class="flex flex-wrap items-end gap-x-6 gap-y-3 rounded-lg border border-line bg-surface-raised p-3 sm:p-4"
      >
        <CertSlot v-if="current" :cert="cert" decorative class="hidden sm:grid" />
        <LifeBar v-if="life" :life="life" large class="min-w-0 flex-[2_1_200px]" />
        <dl v-if="current" class="m-0 grid min-w-0 flex-[3_1_360px] grid-cols-2 gap-x-3 gap-y-2 sm:grid-cols-4 sm:gap-3">
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
              <span v-if="issued && cert.renewalWindow" class="mt-0.5 block text-caption font-normal text-ink-muted [overflow-wrap:anywhere]">
                {{ t('certs.renewal.suggested', { start: intl.date(cert.renewalWindow.start), end: intl.date(cert.renewalWindow.end) }) }}
                <a v-if="cert.renewalWindow.explanationUrl" :href="cert.renewalWindow.explanationUrl" target="_blank" rel="noopener noreferrer">{{
                  t('certs.renewal.why')
                }}</a>
              </span>
              <span class="mt-0.5 flex items-start gap-1 text-caption font-normal text-ink-muted">
                <span class="min-w-0 [overflow-wrap:anywhere]">{{ rule }}</span>
                <button
                  type="button"
                  :aria-label="t('certs.settings.edit')"
                  :title="t('certs.settings.edit')"
                  class="-my-1 grid size-6 flex-none cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-ink-muted hover:bg-surface-sunken hover:text-ink max-sm:-my-2 max-sm:size-8"
                  @click="edit"
                >
                  <Pencil class="size-3.5" aria-hidden="true" />
                </button>
              </span>
            </dd>
          </div>
        </dl>
      </section>

      <!-- Where it is now (deployments, online checks), its history
           (issuances, versions) and what it is (details, renewal settings):
           from 768px the first two on the left, from 1280px each in a
           column of its own, so that the page fits a screen. The last card
           of each group stretches, so that the columns end together. -->
      <div
        class="grid gap-3 sm:gap-4 md:grid-cols-[minmax(0,1fr)_17rem] md:grid-rows-[auto_1fr] xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_18rem] xl:grid-rows-1"
      >
        <div class="flex min-w-0 flex-col gap-3 sm:gap-4 md:col-start-1 md:row-start-1 [&>*:last-child]:flex-1">
          <section aria-labelledby="deploys" class="flex flex-col gap-1.5 rounded-lg border border-line bg-surface-raised p-3 sm:p-4">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <h2 id="deploys" class="m-0 text-heading sm:text-section-title font-semibold">{{ t('certs.detail.deploys') }}</h2>
              <div class="flex flex-wrap gap-2">
                <JecButton v-if="cert.targets.length && current" size="sm" :busy="deploying === 0" @click="startDeployAll">
                  <Send class="size-4" aria-hidden="true" />{{ t('certs.action.deploy_now') }}
                </JecButton>
                <JecButton size="sm" variant="ghost" @click="chooseTargets">
                  <Pencil class="size-4" aria-hidden="true" />{{ t('certs.action.choose_targets') }}
                </JecButton>
              </div>
            </div>
            <JecAlert v-if="deployError.error.value" :code="deployError.error.value.code" :params="deployError.error.value.params" />
            <p v-if="!cert.targets.length" class="m-0 text-small text-ink-muted">{{ t('certs.detail.no_targets') }}</p>
            <!-- Where the card is narrow, a row's state goes under its
                 target, and its button keeps only the icon. -->
            <ol v-else class="@container m-0 list-none p-0 text-small">
              <li v-for="r in deployRows" :key="r.target" class="flex items-center gap-2 border-b border-line last:border-b-0">
                <component
                  :is="r.d ? RouterLink : 'div'"
                  :to="r.d ? `/certificates/${id}/deployments/${r.d.id}` : undefined"
                  class="flex min-h-11 min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-0.5 py-1.5 text-ink no-underline hover:text-ink"
                >
                  <span class="min-w-0 flex-[1_1_100%] [overflow-wrap:anywhere] @md:flex-[1_1_0]">
                    <span class="font-semibold" translate="no">{{ r.label.name }}</span>
                    <span class="ml-1.5 text-ink-muted" :translate="r.label.brand ? 'no' : undefined">{{ r.label.type }}</span>
                    <span v-if="r.shown.length" class="block text-caption text-ink-muted">
                      <span v-for="s in r.shown" :key="s.key" class="not-first:before:mx-1.5 not-first:before:content-['·']">
                        <span v-if="s.label" class="mr-1">{{ t(s.label) }}</span>
                        <template v-if="s.option">{{ t(s.option) }}</template>
                        <span v-else translate="no">{{ s.value }}</span>
                      </span>
                    </span>
                  </span>
                  <span class="flex flex-none items-baseline gap-x-2 @md:flex-col @md:items-end">
                    <span v-if="r.d" :class="['font-semibold', deployInk[r.d.state]]">{{ t(deploymentState(r.d)) }}</span>
                    <span v-else class="text-ink-muted">{{ t('certs.deployment.state.none') }}</span>
                    <time v-if="r.d?.finishedAt" :datetime="r.d.finishedAt" class="text-caption text-ink-muted tabular-nums">{{ intl.dateTime(r.d.finishedAt) }}</time>
                  </span>
                </component>
                <JecButton
                  v-if="current && r.d?.state !== 'running' && r.d?.state !== 'pending'"
                  size="sm"
                  variant="ghost"
                  class="flex-none"
                  :busy="deploying === r.target"
                  :aria-label="t(r.d ? 'certs.action.redeploy_to' : 'certs.action.deploy_to', { target: r.label.name })"
                  :title="t(r.d ? 'certs.action.redeploy_to' : 'certs.action.deploy_to', { target: r.label.name })"
                  @click="redeployTo(r.target, r.d?.state)"
                >
                  <RotateCw class="size-4" aria-hidden="true" /><span class="sr-only @md:not-sr-only">{{ t(r.d ? 'certs.action.redeploy' : 'certs.action.deploy_now') }}</span>
                </JecButton>
              </li>
            </ol>
          </section>

          <MonitorSection v-if="current" :id="cert.id" />
        </div>

        <div class="flex min-w-0 flex-col gap-3 sm:gap-4 md:col-start-1 md:row-start-2 xl:col-start-2 xl:row-start-1 [&>*:last-child]:flex-1">
          <section v-if="issued" aria-labelledby="issuances" class="flex flex-col gap-2 rounded-lg border border-line bg-surface-raised p-3 sm:p-4">
            <h2 id="issuances" class="m-0 text-heading sm:text-section-title font-semibold">{{ t('certs.detail.issuances') }}</h2>
            <div v-if="lastRun.data.value" class="flex flex-col gap-1.5 rounded-md border border-line px-3 py-2.5">
              <div class="flex flex-wrap items-baseline justify-between gap-x-3">
                <h3 class="m-0 text-body font-semibold">
                  {{ t(lastRun.data.value.issuance.reason === 'renewal' ? 'certs.detail.last_renewal' : 'certs.detail.last_issuance') }}
                </h3>
                <RouterLink :to="`/certificates/${id}/issuances/${lastRun.data.value.issuance.id}`" class="text-small">{{ t('certs.detail.full_log') }}</RouterLink>
              </div>
              <p v-if="runSummary" class="m-0 text-small text-ink-muted [overflow-wrap:anywhere]">{{ runSummary }}</p>
              <IssuanceSteps :detail="lastRun.data.value" compact class="text-small" />
            </div>
            <p v-if="issuances.isSuccess.value && !issuances.data.value?.length" class="m-0 text-small text-ink-muted">{{ t('certs.detail.no_issuances') }}</p>
            <ol v-if="earlier.length" class="m-0 max-h-48 list-none overflow-y-auto p-0 text-small">
              <li v-for="is in earlier" :key="is.id" class="border-b border-line last:border-b-0">
                <RouterLink
                  :to="`/certificates/${id}/issuances/${is.id}`"
                  class="flex min-h-9 flex-wrap items-center gap-x-3 gap-y-0.5 py-1.5 text-ink no-underline hover:text-ink"
                >
                  <time :datetime="is.createdAt" class="flex-none text-ink-muted tabular-nums">{{ intl.dateTime(is.createdAt) }}</time>
                  <span class="min-w-[6rem] flex-1 [overflow-wrap:anywhere]">{{ t(reasonText[is.reason]) }}</span>
                  <span :class="['flex-none font-semibold', is.state === 'done' ? 'text-ok-ink' : is.state === 'running' || is.state === 'waiting' || (is.state === 'pending' && !is.error) ? 'text-info-ink' : 'text-danger-ink']">
                    {{ t(issuanceState(is)) }}
                  </span>
                </RouterLink>
              </li>
            </ol>
          </section>
          <section v-if="versions.length" aria-labelledby="versions" class="rounded-lg border border-line bg-surface-raised p-3 sm:p-4">
            <h2 id="versions" class="m-0 mb-1 text-heading sm:text-section-title font-semibold">{{ t('certs.history.title') }}</h2>
            <ol class="m-0 max-h-60 list-none overflow-y-auto p-0 text-small">
              <li v-for="h in history" :key="h.version.id" class="flex items-center gap-1 border-b border-line py-1.5 last:border-b-0">
                <div class="min-w-0 flex-1">
                  <div class="tabular-nums">{{ h.range }}</div>
                  <div class="text-caption text-ink-muted">{{ h.note }}</div>
                  <div v-if="h.version.revokedAt" class="flex items-start gap-1 font-semibold text-danger-ink">
                    <ShieldX class="mt-0.5 size-4 flex-none" aria-hidden="true" />
                    {{ t('certs.history.revoked', { date: intl.date(h.version.revokedAt), reason: t(reasonLabel(h.version.revokeReason)) }) }}
                  </div>
                  <div v-else-if="h.version.caRevokedAt" class="flex items-start gap-1 font-semibold text-danger-ink">
                    <ShieldX class="mt-0.5 size-4 flex-none" aria-hidden="true" />
                    {{ t('certs.history.ca_revoked', { date: intl.date(h.version.caRevokedAt), reason: t(caReasonText(h.version.caRevokeReason)) }) }}
                  </div>
                </div>
                <button
                  v-if="h.version.usable"
                  type="button"
                  :aria-label="t('certs.action.use_version', { range: h.range })"
                  :title="t('certs.action.use_version', { range: h.range })"
                  class="grid size-11 flex-none cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink-muted hover:bg-surface-sunken hover:text-ink sm:size-8"
                  @click="startRevert(h.version.id, h.range, h.version.notAfter)"
                >
                  <Undo2 class="size-4" aria-hidden="true" />
                </button>
                <button
                  v-if="h.version.revocable"
                  type="button"
                  :aria-label="t('certs.action.revoke_version', { range: h.range })"
                  :title="t('certs.action.revoke_version', { range: h.range })"
                  class="grid size-11 flex-none cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink-muted hover:bg-surface-sunken hover:text-danger-ink sm:size-8"
                  @click="startRevoke(h.version.id, h.range)"
                >
                  <ShieldX class="size-4" aria-hidden="true" />
                </button>
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
            <p v-if="sourceType?.revocable && !history.some((h) => h.version.revocable)" class="m-0 mt-1.5 text-caption text-ink-muted">{{ t('certs.history.revoke_hint') }}</p>
          </section>

        </div>

        <div class="flex min-w-0 flex-col gap-3 sm:gap-4 md:col-start-2 md:row-span-2 md:row-start-1 xl:col-start-3 xl:row-span-1 [&>*:last-child]:flex-1">
          <section aria-labelledby="info" class="rounded-lg border border-line bg-surface-raised p-3 sm:p-4">
            <div class="mb-1 flex flex-wrap items-baseline justify-between gap-x-3">
              <h2 id="info" class="m-0 text-heading font-semibold sm:text-section-title">{{ t('certs.info.title') }}</h2>
              <RouterLink :to="{ path: '/certificates/new', query: { from: id } }" class="text-small">{{ t('certs.action.add_like') }}</RouterLink>
            </div>
            <dl class="m-0 grid grid-cols-[fit-content(40%)_minmax(0,1fr)] gap-x-4 gap-y-0.5 text-small">
              <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.memo.title') }}</dt>
              <dd class="m-0 flex min-w-0 items-start gap-1">
                <span v-if="cert.note" class="min-w-0 flex-1 whitespace-pre-line [overflow-wrap:anywhere]" translate="no">{{ cert.note }}</span>
                <span v-else class="min-w-0 flex-1 text-ink-muted">{{ t('certs.memo.none') }}</span>
                <button
                  type="button"
                  :aria-label="t('certs.memo.edit')"
                  :title="t('certs.memo.edit')"
                  class="-my-1 grid size-7 flex-none cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink-muted hover:bg-surface-sunken hover:text-ink max-sm:size-9"
                  @click="editNote"
                >
                  <Pencil class="size-3.5" aria-hidden="true" />
                </button>
              </dd>
              <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.domains') }}</dt>
              <dd class="m-0 flex min-w-0 items-start gap-1">
                <span class="min-w-0 flex-1 [overflow-wrap:anywhere]" translate="no">
                  <span v-for="d in cert.domains" :key="d" class="block">{{ d }}</span>
                </span>
                <button
                  v-if="issued"
                  type="button"
                  :aria-label="t('certs.domains.edit')"
                  :title="t('certs.domains.edit')"
                  class="-my-1 grid size-7 flex-none cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink-muted hover:bg-surface-sunken hover:text-ink max-sm:size-9"
                  @click="editDomains"
                >
                  <Pencil class="size-3.5" aria-hidden="true" />
                </button>
              </dd>
              <template v-if="registrations.length">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('watch.domain.cert_row') }}</dt>
                <dd class="m-0 flex min-w-0 flex-col gap-0.5">
                  <RouterLink
                    v-for="d in registrations"
                    :key="d.name"
                    to="/sites"
                    :class="['[overflow-wrap:anywhere]', domainNeedsLook(d) ? 'font-semibold text-warn-ink' : 'text-ink']"
                  >
                    <span translate="no">{{ d.name }}</span>
                    <template v-if="d.expiresAt">
                      · <time :datetime="d.expiresAt">{{ intl.date(d.expiresAt) }}</time>
                    </template>
                    <template v-else> · {{ t(d.state === 'pending' ? 'watch.domain.state.pending' : 'watch.domain.state.unknown') }}</template>
                  </RouterLink>
                </dd>
              </template>
              <template v-if="details && details.subject !== `CN=${cert.domains[0]}`">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.subject') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]" translate="no">{{ details.subject }}</dd>
              </template>
              <!-- The issuer, and the chain up from it, each with when it expires. -->
              <template v-if="current">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t(chain.length > 2 ? 'certs.chain.title' : 'certs.field.issuer') }}</dt>
                <dd v-if="chain.length > 1" class="m-0 flex min-w-0 flex-col gap-0.5">
                  <span v-for="(c, i) in chain.slice(1)" :key="i" class="[overflow-wrap:anywhere]">
                    <span class="block" translate="no">{{ c.subject }}</span>
                    <span class="block text-caption text-ink-muted">{{ t('certs.chain.expires', { date: intl.date(c.notAfter) }) }}</span>
                  </span>
                </dd>
                <dd v-else class="m-0 [overflow-wrap:anywhere]" translate="no">{{ current.issuer }}</dd>
              </template>
              <template v-if="details?.validation">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.validation_level') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]">{{ t(validationText[details.validation]) }}</dd>
              </template>
              <template v-if="details?.extKeyUsage.length">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.uses') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]">{{ intl.list(details.extKeyUsage.map((u) => t(ekuText[u]))) }}</dd>
              </template>
              <template v-if="details">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.transparency') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]">{{ details.scts ? t('certs.transparency.logged', { count: details.scts }) : t('certs.transparency.none') }}</dd>
              </template>
              <template v-if="details?.mustStaple">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.must_staple') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]">{{ t('certs.must_staple.required') }}</dd>
              </template>
              <template v-if="details && (details.ocsp.length || details.crl.length || details.caIssuers.length)">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.revocation') }}</dt>
                <dd class="m-0 flex flex-wrap">
                  <a
                    v-for="l in revocationLinks"
                    :key="l.href"
                    :href="l.href"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="underline underline-offset-2 not-first:before:mx-1.5 not-first:before:text-ink-muted not-first:before:no-underline not-first:before:content-['·']"
                    >{{ t(l.label) }}</a
                  >
                </dd>
              </template>
              <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.source') }}</dt>
              <dd v-if="issued" class="m-0 [overflow-wrap:anywhere]" translate="no">{{ sourceName ?? '' }}</dd>
              <dd v-else class="m-0 [overflow-wrap:anywhere]">{{ t('certs.source.upload') }}</dd>
              <template v-if="issued && cert.solverProvider">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.validation') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]" translate="no">{{ solverName ?? '' }}</dd>
              </template>
              <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.key') }}</dt>
              <dd v-if="key" class="m-0 [overflow-wrap:anywhere]">{{ t(key.key, key.params) }}</dd>
              <dd v-else class="m-0 [overflow-wrap:anywhere]">{{ t(keyText(cert.keyType).key, keyText(cert.keyType).params) }}</dd>
              <template v-if="details">
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.signature') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]" translate="no">{{ details.signatureAlgorithm }}</dd>
              </template>
              <template v-if="current">
                <template v-for="h in hashes" :key="h.label">
                  <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t(h.label) }}</dt>
                  <dd class="m-0 flex min-w-0 items-start gap-1">
                    <span class="min-w-0 flex-1 truncate font-mono text-caption leading-5" translate="no" :title="h.value">{{ h.value }}</span>
                    <CopyButton :value="h.value" :label="h.copy" />
                  </dd>
                </template>
                <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.csr') }}</dt>
                <dd class="m-0 [overflow-wrap:anywhere]">
                  <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-small text-brand underline underline-offset-2 hover:text-brand-hover" @click="showingCsr = true">
                    {{ t('certs.csr.show') }}
                  </button>
                </dd>
              </template>
              <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.notify') }}</dt>
              <dd v-if="notifying.length" class="m-0 [overflow-wrap:anywhere]" translate="no">{{ intl.list(notifying) }}</dd>
              <dd v-else class="m-0 [overflow-wrap:anywhere] text-ink-muted">{{ t('certs.settings.notify_none') }}</dd>
            </dl>
          </section>

        </div>
      </div>
      <div class="flex flex-wrap gap-2 sm:hidden">
        <JecButton v-if="issued" variant="primary" :busy="issueNow.isPending.value" class="flex-[1_1_auto]" @click="startIssue">
          <ShieldCheck class="size-4" aria-hidden="true" />{{ t(issueLabel) }}
        </JecButton>
        <JecButton v-if="current" class="flex-[1_1_auto]" @click="startDownload(current.id)">
          <Download class="size-4" aria-hidden="true" />{{ t('certs.action.download') }}
        </JecButton>
        <JecButton class="flex-[1_1_auto]" @click="deleting = true"><Trash2 class="size-4" aria-hidden="true" />{{ t('certs.action.delete') }}</JecButton>
      </div>
    </template>
  </div>

  <CertActionDialog v-model:ask="asking" />

  <JecDialog v-if="cert" v-model:open="editing" :title="t('certs.settings.title')" small>
    <form id="settings-form" class="flex flex-col gap-3" novalidate @submit.prevent="saveSettings">
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
      <fieldset v-if="notifiers.data.value?.length" class="m-0 flex flex-col gap-1 border-0 p-0">
        <legend class="mb-1 p-0 font-semibold">{{ t('certs.settings.notify_to') }}</legend>
        <span class="text-small text-ink-muted">{{ t('certs.settings.notify_to_help') }}</span>
        <label v-for="n in notifiers.data.value" :key="n.id" class="flex min-h-9 cursor-pointer items-center gap-2">
          <input v-model="notifyTo" type="checkbox" :value="n.id" class="size-4 flex-none accent-brand" />
          <span class="min-w-0 [overflow-wrap:anywhere]" translate="no">{{ n.name }}</span>
        </label>
      </fieldset>
      <label v-if="keyKept" class="flex cursor-pointer items-start gap-2">
        <input v-model="reuseKey" type="checkbox" class="mt-1 size-4 flex-none accent-brand" />
        <span class="min-w-0">
          <span class="block font-semibold">{{ t('certs.settings.reuse_key') }}</span>
          <span class="block text-small text-ink-muted">{{ t('certs.settings.reuse_key_help') }}</span>
        </span>
      </label>
      <JecAlert v-if="updateError.alert.value" :code="updateError.alert.value.code" :params="updateError.alert.value.params" />
    </form>
    <template #footer>
      <JecButton @click="editing = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="settings-form" variant="primary" :busy="update.isPending.value">{{ t('kernel.action.save') }}</JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert" v-model:open="noting" :title="t('certs.memo.title')" small>
    <form id="note-form" class="flex flex-col gap-3" novalidate @submit.prevent="saveNote">
      <JecField id="note" label="certs.memo.title" hint="certs.memo.help" :error="updateError.fields.value.note">
        <JecTextarea id="note" v-model="note" :rows="3" maxlength="500" :invalid="!!updateError.fields.value.note" />
      </JecField>
      <JecAlert v-if="updateError.alert.value" :code="updateError.alert.value.code" :params="updateError.alert.value.params" />
    </form>
    <template #footer>
      <JecButton @click="noting = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="note-form" variant="primary" :busy="update.isPending.value">{{ t('kernel.action.save') }}</JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert" v-model:open="renaming" :title="t('certs.domains.edit')" small>
    <form id="domains-form" class="flex flex-col gap-3" novalidate @submit.prevent="saveDomains">
      <p class="m-0 text-small text-ink-muted">{{ t('certs.domains.help') }}</p>
      <label for="new-domains" class="sr-only">{{ t('certs.field.domains') }}</label>
      <DomainInput id="new-domains" v-model="newDomains" :invalid="badNewDomains" described-by="new-domains-help" />
      <p v-if="badNewDomains.length" id="new-domains-help" class="m-0 text-small text-danger-ink">{{ t('certs.error.bad_domain') }}</p>
      <p v-else-if="domainsError.fields.value.domains" id="new-domains-help" class="m-0 text-small text-danger-ink">
        {{ t(domainsError.fields.value.domains) }}
      </p>
      <JecAlert v-if="domainsError.alert.value" :code="domainsError.alert.value.code" :params="domainsError.alert.value.params" />
    </form>
    <template #footer>
      <JecButton @click="renaming = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="domains-form" variant="primary" :busy="setDomains.isPending.value" :disabled="!newDomains.length">
        <RotateCw class="size-4" aria-hidden="true" />{{ t('certs.domains.save') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert && current" v-model:open="showingCsr" :title="t('certs.csr.title', { domain })">
    <div class="flex flex-col gap-3">
      <p class="m-0 text-small text-ink-muted">{{ t('certs.csr.intro') }}</p>
      <JecAlert v-if="csrError.error.value" :code="csrError.error.value.code" :params="csrError.error.value.params" />
      <template v-if="csr.data.value">
        <dl class="m-0 grid grid-cols-[fit-content(40%)_minmax(0,1fr)] gap-x-4 gap-y-0.5 text-small">
          <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.subject') }}</dt>
          <dd class="m-0 [overflow-wrap:anywhere]" translate="no">{{ csr.data.value.subject }}</dd>
          <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.csr.names') }}</dt>
          <dd class="m-0 [overflow-wrap:anywhere]" translate="no">
            <span v-for="n in csr.data.value.names" :key="n" class="block">{{ n }}</span>
          </dd>
          <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.key') }}</dt>
          <dd class="m-0 [overflow-wrap:anywhere]">{{ t(keyText(csr.data.value.keyType).key, keyText(csr.data.value.keyType).params) }}</dd>
          <dt class="text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.field.signature') }}</dt>
          <dd class="m-0 [overflow-wrap:anywhere]" translate="no">{{ csr.data.value.signatureAlgorithm }}</dd>
        </dl>
        <div class="relative">
          <pre
            translate="no"
            tabindex="0"
            :aria-label="t('certs.field.csr')"
            class="m-0 max-h-64 overflow-auto rounded-md bg-surface-sunken p-3 pr-10 font-mono text-caption whitespace-pre"
          >{{ csr.data.value.csr }}</pre>
          <span class="absolute top-1.5 right-1.5"><CopyButton :value="csr.data.value.csr" label="certs.csr.copy" /></span>
        </div>
      </template>
    </div>
    <template #footer>
      <JecButton @click="showingCsr = false">{{ t('kernel.action.close') }}</JecButton>
      <JecButton variant="primary" :disabled="!csr.data.value" @click="downloadCsr">
        <Download class="size-4" aria-hidden="true" />{{ t('certs.csr.download') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert" v-model:open="uploading" :title="t('certs.upload.title')">
    <form id="upload-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitUpload">
      <p class="m-0 text-ink-muted">{{ t('certs.upload.intro', { domain }) }}</p>
      <JecAlert v-if="uploadError.alert.value" :code="uploadError.alert.value.code" :params="uploadError.alert.value.params" />
      <PemFields
        v-model:certificate="pem.certificate"
        v-model:private-key="pem.privateKey"
        v-model:pfx="pem.pfx"
        v-model:pfx-password="pem.pfxPassword"
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
      <label class="flex cursor-pointer items-start gap-2">
        <input v-model="pfx.on" type="checkbox" class="mt-1 size-4 flex-none accent-brand" />
        <span class="min-w-0">
          <span class="block font-semibold">{{ t('certs.download.pfx') }}</span>
          <span class="block text-small text-ink-muted">{{ t('certs.download.pfx_help') }}</span>
        </span>
      </label>
      <template v-if="pfx.on">
        <JecField id="pfx-password" label="certs.download.pfx_password" hint="certs.download.pfx_password_help" :error="downloadError.fields.value.pfxPassword">
          <JecInput id="pfx-password" v-model="pfx.password" type="password" autocomplete="new-password" />
        </JecField>
        <label v-if="pfx.password" class="flex cursor-pointer items-start gap-2">
          <input v-model="pfx.legacy" type="checkbox" class="mt-1 size-4 flex-none accent-brand" />
          <span class="min-w-0">
            <span class="block font-semibold">{{ t('certs.download.pfx_legacy') }}</span>
            <span class="block text-small text-ink-muted">{{ t('certs.download.pfx_legacy_help') }}</span>
          </span>
        </label>
      </template>
    </form>
    <template #footer>
      <JecButton @click="downloadOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="download-form" variant="primary" :busy="download.isPending.value" :disabled="!password">
        <Download class="size-4" aria-hidden="true" />{{ t('certs.action.download') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert" v-model:open="revokeOpen" :title="t('certs.revoke.title')" small>
    <form id="revoke-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitRevoke">
      <p class="m-0">{{ t('certs.revoke.intro', { range: revoking?.range ?? '' }) }}</p>
      <JecAlert v-if="revokeError.alert.value" :code="revokeError.alert.value.code" :params="revokeError.alert.value.params" />
      <JecField id="revoke-reason" label="certs.revoke.reason">
        <select id="revoke-reason" v-model="revokeReason" class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink max-sm:h-11">
          <option v-for="r in reasons" :key="r.value" :value="r.value">{{ t(r.label) }}</option>
        </select>
      </JecField>
      <JecField id="revoke-password" label="kernel.account.confirm_password" :error="revokeError.fields.value.password">
        <JecInput
          id="revoke-password"
          v-model="revokePassword"
          type="password"
          autocomplete="current-password"
          :invalid="!!revokeError.fields.value.password"
          required
        />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="revokeOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="revoke-form" variant="danger" :busy="revoke.isPending.value" :disabled="!revokePassword">
        <ShieldX class="size-4" aria-hidden="true" />{{ t('certs.action.revoke') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-if="cert" v-model:open="revertOpen" :title="t('certs.use.title')" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('certs.use.intro', { range: reverting?.range ?? '' }) }}</p>
      <p class="m-0 text-small text-ink-muted">
        {{ t('certs.use.renewal', { date: reverting ? intl.date(reverting.notAfter) : '' }) }}
      </p>
      <JecAlert v-if="useVersionError.error.value" :code="useVersionError.error.value.code" :params="useVersionError.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="revertOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="primary" :busy="useVersion.isPending.value" @click="submitRevert">
        <Undo2 class="size-4" aria-hidden="true" />{{ t('certs.use.confirm') }}
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
