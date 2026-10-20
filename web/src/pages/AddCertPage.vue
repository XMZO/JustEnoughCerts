<script setup lang="ts">
import { CircleCheck, CircleX, FilePlus, Plus, Save, ShieldCheck, TriangleAlert, Upload } from '@lucide/vue'
import { computed, ref, watch, watchEffect } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { ApiError, networkError } from '../api/client'
import {
  useAddCertificate,
  useCertificates,
  useCertSettings,
  useImportCertificates,
  useIssueCertificate,
  useRequestCertificate,
  type Imported,
  type KeyType,
} from '../api/certs'
import { revealError, useApiError } from '../api/errors'
import { useAccountDomains, usePreflight, useProviders, useProviderTypes, useZones, type Provider } from '../api/providers'
import { loadLastChoice, saveLastChoice, type LastChoice } from '../certs/lastChoice'
import { keyText } from '../certs/life'
import DomainInput from '../components/DomainInput.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import JecTextarea from '../components/JecTextarea.vue'
import PageHeader from '../components/PageHeader.vue'
import PemFields from '../components/PemFields.vue'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'
import { typeName } from '../providers/fields'

// Adding a certificate: one page, no wizard (docs/design/boards/AddCert).
// The source is an upload or a configured source, such as an ACME account;
// one that proves control of the domains asks for the solver to do it
// with, a DNS provider or a website directory, of a kind the source takes.
// The plan on the side says what JEC will do.
const { t } = useT()
const intl = useIntl()
const router = useRouter()
const route = useRoute()

const sources = useProviders('source')
const sourceTypes = useProviderTypes('source')
const solvers = useProviders('solver')
const solverTypes = useProviderTypes('solver')
const targets = useProviders('target')
const targetTypes = useProviderTypes('target')
const chosenTargets = ref<number[]>([])
// What the new certificate starts from: a certificate the link names
// (?from=<id>), as "add one like this" does, or else what the last one
// was added with, in this browser. The targets start from those still
// there.
const from = Number(route.query.from) || 0
const certificates = useCertificates()
const template = computed(() => (from ? certificates.data.value?.find((c) => c.id === from) : undefined))
const templateReady = computed(() => !from || certificates.isSuccess.value || certificates.isError.value)
const stored = loadLastChoice()
const last = computed<LastChoice>(() =>
  template.value
    ? { source: template.value.sourceProvider, solver: template.value.solverProvider, backup: template.value.backupProvider, targets: template.value.targets }
    : from
      ? {}
      : stored,
)
let targetsSet = false
watchEffect(() => {
  const list = targets.data.value
  if (targetsSet || !list || !templateReady.value) return
  targetsSet = true
  chosenTargets.value = (last.value.targets ?? []).filter((id) => list.some((p) => p.id === id))
})

// The choice of source: the one of the certificate started from, or the
// first configured one, until someone picks; or an upload when the link
// asks for one, as importing does, or the certificate started from is one.
const picked = ref<'upload' | 'request' | number | undefined>(route.query.source === 'upload' ? 'upload' : undefined)
const source = computed<'upload' | 'request' | number | undefined>(() => {
  if (picked.value !== undefined) return picked.value
  if (!sources.isSuccess.value || !templateReady.value) return undefined
  if (template.value?.source === 'upload') return 'upload'
  const list = sources.data.value ?? []
  return list.find((p) => p.id === last.value.source)?.id ?? list[0]?.id ?? 'upload'
})
const provider = computed(() => (typeof source.value === 'number' ? sources.data.value?.find((p) => p.id === source.value) : undefined))
const sourceType = computed(() => sourceTypes.data.value?.find((s) => s.id === provider.value?.type))
const needsSolver = computed(() => !!sourceType.value?.needsSolver)
// A source with keys of its own, such as the directory of another ACME
// client: no key type to choose, and renewing is taking the new one in.
const ownKey = computed(() => !!sourceType.value?.ownKey)
// How the solvers prove control of the domains: through DNS or files on
// the website. A source whose validation has records of any type, as a
// vendor's, takes a solver of DNS that adds them.
type Challenge = 'dns' | 'http' | 'record'
const accepted = computed<Challenge[]>(() => sourceType.value?.challenges ?? ['dns'])
const solverType = (p: Provider) => solverTypes.data.value?.find((s) => s.id === p.type)
const challengeOf = (p: Provider): 'dns' | 'http' => solverType(p)?.challenge ?? 'dns'
const takesWith = (kinds: Challenge[], p: Provider) =>
  kinds.includes(challengeOf(p)) || (challengeOf(p) === 'dns' && !!solverType(p)?.records && kinds.includes('record'))
const takes = (p: Provider) => takesWith(accepted.value, p)
const usable = computed(() => (solvers.data.value ?? []).filter(takes))
// The kinds of solver that add records of any type, for a source that has none to use.
const recordKinds = computed(() =>
  intl.list(
    (solverTypes.data.value ?? [])
      .filter((s) => s.records)
      .map((s) => {
        const n = typeName(s)
        return n.brand ?? (n.key ? t(n.key) : s.id)
      }),
  ),
)

const typeLabel = (p: Provider, types: typeof sourceTypes) => {
  const n = typeName(types.data.value?.find((s) => s.id === p.type))
  return { brand: n.brand, text: n.key ? t(n.key) : (n.brand ?? '') }
}

// Upload.
const add = useAddCertificate()
const pem = ref({ certificate: '', privateKey: '', pfx: '', pfxPassword: '' })

// Issue: for the domains the link names (?domains=a,b), as a certificate
// found in transparency logs offers, if any.
const issue = useIssueCertificate()
// Or a certificate to buy by hand: JEC makes the key and the CSR for it.
const request = useRequestCertificate()
const domains = ref<string[]>(
  String(route.query.domains ?? '')
    .split(',')
    .map((d) => d.trim().toLowerCase())
    .filter(Boolean)
    .slice(0, 100),
)
// A wildcard covers the names under a domain, not the domain itself: the
// page offers to add those left out, a common slip.
const missingApex = computed(() =>
  domains.value.filter((d) => d.startsWith('*.') && !domains.value.includes(d.slice(2))).map((d) => d.slice(2)),
)
const addApex = () => (domains.value = [...domains.value, ...missingApex.value])
// Or several at once: a certificate a line, its domains apart by spaces or
// commas, all else alike.
const bulk = ref(false)
const lines = ref('')
const batch = computed(() =>
  lines.value
    .split('\n')
    .map((l) => l.split(/[\s,\uFF0C\u3001]+/).filter(Boolean))
    .filter((d) => d.length > 0),
)
interface Outcome {
  domains: string[]
  message: string
}
// The lines that could not be added, and why.
const refused = ref<Outcome[]>([])
const adding = ref(false)
const pickedSolver = ref<number>()
const solver = computed(
  () => usable.value.find((p) => p.id === (pickedSolver.value ?? last.value.solver))?.id ?? usable.value[0]?.id,
)
const solverProvider = computed(() => usable.value.find((p) => p.id === solver.value))
// A source to fall back on, as an ACME account of another CA: the next
// attempt goes to it when one with the source failed. Those of a type that
// can, taking the solver.
const backups = computed(() =>
  ownKey.value
    ? []
    : (sources.data.value ?? []).filter((p) => {
        const typ = sourceTypes.data.value?.find((s) => s.id === p.type)
        if (p.id === source.value || !typ?.backup) return false
        return !typ.needsSolver || (!!solverProvider.value && takesWith(typ.challenges ?? ['dns'], solverProvider.value))
      }),
)
const pickedBackup = ref<number>()
const backup = computed(() => backups.value.find((p) => p.id === (pickedBackup.value ?? last.value.backup))?.id ?? 0)
// Whether the solver's account holds the zones of the domains, or whether
// their name servers are its service's, for a type that can tell: a domain
// it cannot prove fails the issuance.
const zoneDomains = computed(() => [...new Set(bulk.value ? batch.value.flat() : domains.value)].slice(0, 50))
const zonesTold = computed(() => {
  const typ = solverProvider.value && solverType(solverProvider.value)
  return !!typ && (!!typ.zones || !!typ.nameServers?.length)
})
const zones = useZones(solver, zoneDomains, () => needsSolver.value && zonesTold.value)
const notHeld = computed(() => (zones.data.value ?? []).filter((z) => z.held === false))
const elsewhere = computed(() => (zones.data.value ?? []).filter((z) => z.served === false && z.held !== false))
// The domains of the solver's account, to pick from rather than type: those
// not chosen yet.
const account = useAccountDomains(solver, () => needsSolver.value && !bulk.value && !!solverProvider.value && !!solverType(solverProvider.value)?.zones)
const offered = computed(() => (account.data.value?.domains ?? []).filter((d) => !domains.value.includes(d)).slice(0, 20))
const pickDomain = (d: string) => (domains.value = [...domains.value, d])
const allHeld = computed(() => !!zones.data.value?.length && zones.data.value.every((z) => z.held === true))
// What the solver's last test said, as its card says it.
const solverTest = computed(() => {
  const last = solverProvider.value?.lastTest
  if (!last) return undefined
  const said = t(last.message.key as MessageKey, (last.message.params ?? {}) as Params)
  return { ok: last.ok, text: last.ok ? said : t('kernel.providers.status.test_failed', { error: said }) }
})
// The words of the solver field: for the kind the source takes, or the one picked.
const only = computed<Challenge | undefined>(() => (accepted.value.length === 1 ? accepted.value[0] : undefined))
const solverLabel = computed<MessageKey>(() =>
  only.value === 'dns' || only.value === 'record' ? 'certs.add.dns' : only.value === 'http' ? 'certs.add.http' : 'certs.add.solver',
)
const solverHint = computed<MessageKey>(() => {
  if (solverProvider.value && solverType(solverProvider.value)?.manual) return 'certs.add.manual_help'
  if (solverProvider.value && challengeOf(solverProvider.value) === 'http') return 'certs.add.http_help'
  return accepted.value.includes('dns') ? 'certs.add.dns_help' : 'certs.add.records_help'
})
const noSolver = computed<MessageKey>(() => {
  if (accepted.value.includes('record') && !accepted.value.includes('dns')) {
    return accepted.value.includes('http') ? 'certs.add.no_http_records' : 'certs.add.no_records'
  }
  return only.value === 'dns' ? 'certs.add.no_dns' : only.value === 'http' ? 'certs.add.no_http' : 'certs.add.no_solver'
})
const keyTypes: KeyType[] = ['rsa-2048', 'ecdsa-p256', 'ecdsa-p384', 'rsa-3072', 'rsa-4096']
const keyType = ref<KeyType>('rsa-2048')
const keyLabel = (k: string) => {
  const text = keyText(k)
  return t(text.key, text.params)
}

const days = ref('30')
const renewDays = computed(() => Number(days.value) || 30)

// The defaults of the settings, taken once they come, before anyone could
// have changed the fields.
const settings = useCertSettings()
let defaulted = false
watchEffect(() => {
  const st = settings.data.value
  if (!st || defaulted || !templateReady.value) return
  defaulted = true
  const like = template.value
  keyType.value = like && keyTypes.includes(like.keyType as KeyType) ? (like.keyType as KeyType) : st.keyType
  days.value = String(like?.renewBeforeDays ?? st.renewBeforeDays)
})

const uploading = computed(() => source.value === 'upload')
const requesting = computed(() => source.value === 'request')
watch(requesting, (now) => {
  if (now) bulk.value = false
})
// Or many at once, from the files another tool exported: certificates and
// keys in any mix, which the server pairs.
const importing = ref(false)
const importFiles = ref<File[]>([])
const importer = useImportCertificates()
const importError = useApiError(computed(() => importer.error.value))
const imported = ref<{ certificates: Imported[]; files: { name: string; code: string }[] }>()
const importLooks: Record<Imported['result'], { icon: typeof CircleCheck; ink: string; label: MessageKey }> = {
  added: { icon: CircleCheck, ink: 'text-ok-ink', label: 'certs.import.added' },
  duplicate: { icon: CircleCheck, ink: 'text-ink-muted', label: 'certs.import.duplicate' },
  no_key: { icon: TriangleAlert, ink: 'text-warn-ink', label: 'certs.import.no_key' },
  failed: { icon: CircleX, ink: 'text-danger-ink', label: 'certs.import.failed' },
}
function pickImport(e: Event) {
  importFiles.value = [...((e.target as HTMLInputElement).files ?? [])]
  imported.value = undefined
}
// base64 of a file, which may be binary, as DER is.
async function base64Of(f: File): Promise<string> {
  const bytes = new Uint8Array(await f.arrayBuffer())
  let s = ''
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  return btoa(s)
}
async function submitImport() {
  const files = await Promise.all(importFiles.value.map(async (f) => ({ name: f.name, content: await base64Of(f) })))
  try {
    imported.value = await importer.mutateAsync({ files, renewBeforeDays: Number(days.value) || undefined, targets: chosenTargets.value })
  } catch {
    // shown above the files
  }
}
const mutation = computed(() => (uploading.value ? add : requesting.value ? request : issue))
const errors = useApiError(computed(() => mutation.value.error.value))
const badTargets = computed(() => Object.entries(errors.fields.value).find(([f]) => f.startsWith('targets'))?.[1])
const badDomains = computed(() =>
  Object.keys(errors.fields.value)
    .map((f) => /^domains\[(\d+)\]$/.exec(f)?.[1])
    .filter((i): i is string => i !== undefined)
    .map(Number),
)
// What the source sees wrong with the domains before issuing, as CAA
// records that leave its CA out.
const preflight = usePreflight(
  computed(() => (typeof source.value === 'number' ? source.value : undefined)),
  zoneDomains,
  () => typeof source.value === 'number' && !badDomains.value.length,
)

const deploySteps = computed(() =>
  chosenTargets.value.map((id) => t('certs.plan.deploy', { target: targets.data.value?.find((p) => p.id === id)?.name ?? '' })),
)
const plan = computed(() => {
  if (uploading.value) {
    return [t('certs.plan.check'), t('certs.plan.store'), ...deploySteps.value, t('certs.plan.remind', { days: renewDays.value })]
  }
  if (requesting.value) {
    return [
      t('certs.plan.key', { key: keyLabel(keyType.value) }),
      t('certs.plan.csr'),
      t('certs.plan.pair'),
      ...deploySteps.value,
      t('certs.plan.remind', { days: renewDays.value }),
    ]
  }
  const time = settings.data.value?.checkTime ?? '02:00'
  if (ownKey.value) {
    return [
      t('certs.plan.collect', { source: provider.value?.name ?? '' }),
      ...deploySteps.value,
      t('certs.plan.watch', { days: renewDays.value, time }),
    ]
  }
  const steps = [t('certs.plan.key', { key: keyLabel(keyType.value) }), t('certs.plan.order', { source: provider.value?.name ?? '' })]
  if (bulk.value) steps.unshift(t('certs.plan.bulk', { count: batch.value.length }))
  if (needsSolver.value && solverProvider.value) {
    const name = solverProvider.value.name
    steps.push(
      solverType(solverProvider.value)?.manual
        ? t('certs.plan.manual')
        : challengeOf(solverProvider.value) === 'http'
          ? t('certs.plan.http', { solver: name })
          : t('certs.plan.dns', { dns: name }),
    )
  }
  steps.push(t('certs.plan.download'))
  steps.push(...deploySteps.value, t('certs.plan.renew', { days: renewDays.value, time }))
  return steps
})

const form = ref<HTMLFormElement>()

// Keeps what this certificate is added with, for the next one.
const remember = () =>
  saveLastChoice({
    source: typeof source.value === 'number' ? source.value : undefined,
    solver: needsSolver.value ? solver.value : undefined,
    backup: backup.value || undefined,
    targets: chosenTargets.value,
  })

async function submit() {
  if (uploading.value && importing.value) {
    await submitImport()
    return
  }
  if (uploading.value) {
    let created
    try {
      created = await add.mutateAsync({
        certificate: pem.value.certificate || undefined,
        privateKey: pem.value.privateKey || undefined,
        pfx: pem.value.pfx || undefined,
        pfxPassword: pem.value.pfxPassword || undefined,
        renewBeforeDays: Number(days.value) || undefined,
        targets: chosenTargets.value,
      })
    } catch {
      await revealError(form.value) // shown by the fields and the alert
      return
    }
    await router.replace(`/certificates/${created.id}`)
    return
  }
  if (requesting.value) {
    let created
    try {
      created = await request.mutateAsync({ domains: domains.value, keyType: keyType.value, renewBeforeDays: Number(days.value) || undefined, targets: chosenTargets.value })
    } catch {
      await revealError(form.value)
      return
    }
    await router.replace({ path: `/certificates/${created.id}`, query: { csr: '1' } })
    return
  }
  if (typeof source.value !== 'number') return
  if (bulk.value) {
    remember()
    await submitBatch(source.value)
    return
  }
  remember()
  let created
  try {
    created = await issue.mutateAsync({
      domains: domains.value,
      sourceProvider: source.value,
      solverProvider: needsSolver.value ? solver.value : undefined,
      backupProvider: backup.value || undefined,
      keyType: ownKey.value ? undefined : keyType.value,
      renewBeforeDays: Number(days.value) || undefined,
      targets: chosenTargets.value,
    })
  } catch {
    await revealError(form.value)
    return
  }
  await router.replace(created.issuance ? `/certificates/${created.id}/issuances/${created.issuance.id}` : `/certificates/${created.id}`)
}

// submitBatch adds a certificate for each line, one after the other: those
// that could not be added stay in the box, with why, for another go.
async function submitBatch(from: number) {
  adding.value = true
  refused.value = []
  const failed: Outcome[] = []
  try {
    for (const d of batch.value) {
      try {
        await issue.mutateAsync({
          domains: d,
          sourceProvider: from,
          solverProvider: needsSolver.value ? solver.value : undefined,
          backupProvider: backup.value || undefined,
          keyType: ownKey.value ? undefined : keyType.value,
          renewBeforeDays: Number(days.value) || undefined,
          targets: chosenTargets.value,
        })
      } catch (err) {
        const e = err instanceof ApiError ? err : networkError
        const field = e.fields[0]
        failed.push({ domains: d, message: field ? t(field.code) : t(e.code, e.params) })
      }
    }
  } finally {
    adding.value = false
    issue.reset()
  }
  if (failed.length === 0) {
    await router.replace('/certificates')
    return
  }
  refused.value = failed
  lines.value = failed.map((f) => f.domains.join(' ')).join('\n')
}

const cardClass = (on: boolean) => [
  'grid min-h-16 cursor-pointer content-start gap-0.5 rounded-md border px-3 py-2.5 text-left text-ink',
  on ? 'border-brand bg-brand-soft shadow-[inset_0_0_0_1px_var(--brand)]' : 'border-line-strong bg-surface-raised hover:bg-surface-sunken',
]
const fieldError = (name: string): MessageKey | undefined => errors.fields.value[name]
</script>

<template>
  <PageHeader :title="t('certs.action.add')" :subtitle="t('certs.add.intro')" :back="{ to: '/certificates', label: 'certs.list.title' }" />

  <div class="flex flex-wrap items-start gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <form id="add-form" ref="form" class="flex min-w-0 flex-[999_1_560px] flex-col gap-3 sm:gap-4" novalidate @submit.prevent="submit">
      <section aria-labelledby="source" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
        <h2 id="source" class="m-0 text-section-title font-semibold">{{ t('certs.add.source') }}</h2>
        <div role="radiogroup" aria-labelledby="source" class="grid grid-cols-[repeat(auto-fill,minmax(196px,1fr))] gap-2 max-sm:grid-cols-2">
          <button
            v-for="p in sources.data.value ?? []"
            :key="p.id"
            type="button"
            role="radio"
            :aria-checked="source === p.id ? 'true' : 'false'"
            :class="cardClass(source === p.id)"
            @click="picked = p.id"
          >
            <span class="truncate text-body font-semibold" translate="no" :title="p.name">{{ p.name }}</span>
            <span
              class="truncate text-caption leading-[18px] text-ink-muted"
              :translate="typeLabel(p, sourceTypes).brand ? 'no' : undefined"
              :title="typeLabel(p, sourceTypes).text"
            >
              {{ typeLabel(p, sourceTypes).text }}
            </span>
          </button>
          <button type="button" role="radio" :aria-checked="uploading ? 'true' : 'false'" :class="cardClass(uploading)" @click="picked = 'upload'">
            <span class="text-body font-semibold">{{ t('certs.source.upload') }}</span>
            <span class="text-caption leading-[18px] text-ink-muted">{{ t('certs.source.upload_desc') }}</span>
          </button>
          <button type="button" role="radio" :aria-checked="requesting ? 'true' : 'false'" :class="cardClass(requesting)" @click="picked = 'request'">
            <span class="text-body font-semibold">{{ t('certs.source.request') }}</span>
            <span class="text-caption leading-[18px] text-ink-muted">{{ t('certs.source.request_desc') }}</span>
          </button>
          <RouterLink
            to="/sources"
            class="grid min-h-16 content-start gap-0.5 rounded-md border border-dashed border-line-strong px-3 py-2.5 text-ink no-underline hover:bg-surface-sunken hover:text-ink"
          >
            <span class="flex items-center gap-1.5 text-body font-semibold"><Plus class="size-4" aria-hidden="true" />{{ t('certs.add.new_source') }}</span>
            <span class="text-caption leading-[18px] text-ink-muted">{{ t('certs.add.new_source_desc') }}</span>
          </RouterLink>
        </div>
        <JecAlert v-if="errors.alert.value" :code="errors.alert.value.code" :params="errors.alert.value.params" />
        <div v-if="uploading" role="group" :aria-label="t('certs.import.mode')" class="inline-flex gap-0.5 self-start rounded-md border border-line bg-surface-sunken p-0.5">
          <button
            v-for="many in [false, true]"
            :key="String(many)"
            type="button"
            :aria-pressed="importing === many ? 'true' : 'false'"
            class="h-[30px] cursor-pointer rounded-sm border-0 bg-transparent px-3 text-small font-semibold text-ink-muted hover:text-ink aria-pressed:bg-surface-raised aria-pressed:text-ink aria-pressed:shadow-[0_0_0_1px_var(--line-strong)]"
            @click="importing = many"
          >
            {{ t(many ? 'certs.import.many' : 'certs.import.one') }}
          </button>
        </div>
        <div v-if="uploading && importing" class="flex flex-col gap-2">
          <JecField id="import-files" label="certs.import.files" hint="certs.import.files_help">
            <input
              id="import-files"
              type="file"
              multiple
              accept=".pem,.crt,.cer,.der,.key,.txt"
              class="block w-full min-w-0 text-small text-ink file:mr-3 file:h-9 file:cursor-pointer file:rounded-md file:border file:border-solid file:border-line-strong file:bg-surface-raised file:px-3 file:font-semibold file:text-ink hover:file:bg-surface-sunken"
              @change="pickImport"
            />
          </JecField>
          <JecAlert v-if="importError.error.value" :code="importError.error.value.code" :params="importError.error.value.params" />
          <section v-if="imported" aria-labelledby="import-results" role="status" class="flex flex-col gap-1.5">
            <h3 id="import-results" class="m-0 text-body font-semibold">
              {{ t('certs.import.summary', { added: imported.certificates.filter((c) => c.result === 'added').length, total: imported.certificates.length }) }}
            </h3>
            <ul class="m-0 flex list-none flex-col gap-1 p-0 text-small">
              <li v-for="(c, i) in imported.certificates" :key="i" class="flex items-start gap-2">
                <component :is="importLooks[c.result].icon" :class="['mt-0.5 size-4 flex-none', importLooks[c.result].ink]" aria-hidden="true" />
                <span class="min-w-0 flex-1 [overflow-wrap:anywhere]">
                  <RouterLink v-if="c.id" :to="`/certificates/${c.id}`" class="font-semibold" translate="no">{{ c.domains[0] }}</RouterLink>
                  <span v-else class="font-semibold" translate="no">{{ c.domains[0] }}</span>
                  <span class="ml-2 text-ink-muted">{{ t(importLooks[c.result].label) }}</span>
                </span>
              </li>
              <li v-for="f in imported.files" :key="f.name" class="flex items-start gap-2">
                <TriangleAlert class="mt-0.5 size-4 flex-none text-warn-ink" aria-hidden="true" />
                <span class="min-w-0 flex-1 [overflow-wrap:anywhere]">
                  <span class="font-semibold" translate="no">{{ f.name }}</span>
                  <span class="ml-2 text-ink-muted">{{ t(f.code as MessageKey) }}</span>
                </span>
              </li>
            </ul>
          </section>
        </div>
        <PemFields
          v-if="uploading && !importing"
          v-model:certificate="pem.certificate"
          v-model:private-key="pem.privateKey"
          v-model:pfx="pem.pfx"
          v-model:pfx-password="pem.pfxPassword"
          :errors="errors.fields.value"
          :error-params="errors.error.value?.params"
        />
        <JecField
          v-else-if="(provider && !ownKey) || requesting"
          id="keyType"
          label="certs.add.key_type"
          :hint="requesting ? 'certs.request.key_help' : 'certs.add.key_type_help'"
          :error="fieldError('keyType')"
        >
          <select
            id="keyType"
            v-model="keyType"
            class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink max-sm:h-11 sm:max-w-60"
          >
            <option v-for="k in keyTypes" :key="k" :value="k">{{ keyLabel(k) }}</option>
          </select>
        </JecField>
        <JecField v-if="provider && backups.length" id="backup" label="certs.add.backup" hint="certs.add.backup_help" :error="fieldError('backupProvider')">
          <select
            id="backup"
            :value="backup"
            class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink max-sm:h-11 sm:max-w-96"
            @change="pickedBackup = Number(($event.target as HTMLSelectElement).value)"
          >
            <option :value="0">{{ t('certs.add.backup_none') }}</option>
            <option v-for="p in backups" :key="p.id" :value="p.id" translate="no">{{ p.name }} · {{ typeLabel(p, sourceTypes).text }}</option>
          </select>
        </JecField>
      </section>

      <template v-if="provider || requesting">
        <section aria-labelledby="domains" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 id="domains" class="m-0 text-section-title font-semibold">{{ t('certs.add.domains') }}</h2>
            <div v-if="!requesting" role="group" :aria-label="t('certs.add.bulk_mode')" class="inline-flex gap-0.5 rounded-md border border-line bg-surface-sunken p-0.5">
              <button
                v-for="many in [false, true]"
                :key="String(many)"
                type="button"
                :aria-pressed="bulk === many ? 'true' : 'false'"
                class="h-[30px] cursor-pointer rounded-sm border-0 bg-transparent px-3 text-[13px] font-semibold whitespace-nowrap text-ink-muted hover:text-ink aria-pressed:bg-surface-raised aria-pressed:text-ink aria-pressed:shadow-[0_0_0_1px_var(--line-strong)] max-sm:h-auto max-sm:min-h-9"
                @click="bulk = many"
              >
                {{ t(many ? 'certs.add.bulk_many' : 'certs.add.bulk_one') }}
              </button>
            </div>
          </div>
          <div v-if="bulk" class="grid gap-1.5">
            <label for="bulk-input" class="sr-only">{{ t('certs.add.bulk_label') }}</label>
            <JecTextarea id="bulk-input" v-model="lines" :rows="6" spellcheck="false" aria-describedby="bulk-help" />
            <p id="bulk-help" class="m-0 text-small text-ink-muted">{{ t('certs.add.bulk_help', { count: batch.length }) }}</p>
            <div v-if="refused.length" role="alert" class="flex flex-col gap-1 rounded-md border border-line px-3 py-2 text-small">
              <p class="m-0 font-semibold text-danger-ink">{{ t('certs.add.bulk_failed', { count: refused.length }) }}</p>
              <p v-for="(r, i) in refused" :key="i" class="m-0 flex items-start gap-1.5 [overflow-wrap:anywhere]">
                <CircleX class="mt-0.5 size-4 flex-none text-danger-ink" aria-hidden="true" />
                <span><span class="font-semibold" translate="no">{{ r.domains.join(' ') }}</span> · {{ r.message }}</span>
              </p>
            </div>
          </div>
          <div v-else class="grid gap-1.5">
            <label for="domain-input" class="sr-only">{{ t('certs.add.domains') }}</label>
            <DomainInput id="domain-input" v-model="domains" :invalid="badDomains" described-by="domains-help" />
            <p v-if="badDomains.length" id="domains-help" class="m-0 text-small text-danger-ink">{{ t('certs.error.bad_domain') }}</p>
            <p v-else-if="fieldError('domains')" id="domains-help" class="m-0 text-small text-danger-ink">{{ t(fieldError('domains')!) }}</p>
            <p v-else id="domains-help" class="m-0 text-small text-ink-muted">{{ t('certs.add.domains_help') }}</p>
            <p
              v-for="(p, i) in preflight.data.value ?? []"
              :key="i"
              role="status"
              class="m-0 flex items-start gap-1.5 text-small text-warn-ink [overflow-wrap:anywhere]"
            >
              <TriangleAlert class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ t(p.key as MessageKey, (p.params ?? {}) as Params) }}
            </p>
            <p v-if="missingApex.length" role="status" class="m-0 flex flex-wrap items-center gap-x-3 gap-y-1 text-small">
              <span class="[overflow-wrap:anywhere]">{{ t('certs.add.apex_hint', { names: intl.list(missingApex) }) }}</span>
              <JecButton size="sm" @click="addApex"><Plus class="size-4" aria-hidden="true" />{{ t('certs.add.apex_add') }}</JecButton>
            </p>
          </div>
        </section>

        <section v-if="needsSolver" aria-labelledby="validation" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
          <h2 id="validation" class="m-0 text-section-title font-semibold">{{ t('certs.add.validation') }}</h2>
          <template v-if="usable.length">
            <JecField id="solver" :label="solverLabel" :hint="solverHint" :error="fieldError('solverProvider')">
              <select
                id="solver"
                :value="solver"
                class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink max-sm:h-11 sm:max-w-96"
                @change="pickedSolver = Number(($event.target as HTMLSelectElement).value)"
              >
                <option v-for="s in usable" :key="s.id" :value="s.id" translate="no">
                  {{ s.name }} · {{ typeLabel(s, solverTypes).text }}
                </option>
              </select>
            </JecField>
            <p v-if="solverTest" :class="['m-0 flex items-start gap-1.5 text-small [overflow-wrap:anywhere]', solverTest.ok ? 'text-ok-ink' : 'text-warn-ink']">
              <component :is="solverTest.ok ? CircleCheck : TriangleAlert" class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ solverTest.text }}
            </p>
            <p v-for="z in notHeld" :key="z.domain" role="status" class="m-0 flex items-start gap-1.5 text-small text-warn-ink [overflow-wrap:anywhere]">
              <TriangleAlert class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ t('certs.add.zone_missing', { domain: z.domain, zone: z.zone ?? '' }) }}
            </p>
            <p v-for="z in elsewhere" :key="`ns-${z.domain}`" role="status" class="m-0 flex items-start gap-1.5 text-small text-warn-ink [overflow-wrap:anywhere]">
              <TriangleAlert class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ t('certs.add.ns_other', { domain: z.domain, servers: intl.list(z.nameServers ?? []) }) }}
            </p>
            <p v-if="allHeld" role="status" class="m-0 flex items-start gap-1.5 text-small text-ok-ink">
              <CircleCheck class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ t('certs.add.zones_held') }}
            </p>
            <p v-if="allHeld || notHeld.length" class="m-0 text-caption text-ink-muted">{{ t('certs.add.zones_untested') }}</p>
            <div v-if="offered.length" class="flex flex-col gap-1">
              <span class="text-small text-ink-muted">{{ t('certs.add.account_domains', { count: account.data.value?.total ?? 0 }) }}</span>
              <span class="text-caption text-ink-muted">{{ t('certs.add.account_domains_untested') }}</span>
              <span class="flex flex-wrap gap-1.5">
                <JecButton v-for="d in offered" :key="d" size="sm" :aria-label="t('certs.add.pick_domain', { domain: d })" @click="pickDomain(d)">
                  <Plus class="size-4" aria-hidden="true" /><span translate="no">{{ d }}</span>
                </JecButton>
              </span>
            </div>
            <RouterLink to="/dns" class="self-start text-small">{{ t('certs.add.manage_solvers') }}</RouterLink>
          </template>
          <div v-else class="flex flex-col items-start gap-2">
            <p :class="['m-0', fieldError('solverProvider') ? 'text-danger-ink' : 'text-ink-muted']">
              {{ t(noSolver, { providers: recordKinds }) }}
            </p>
            <JecButton to="/dns" size="sm"><Plus class="size-4" aria-hidden="true" />{{ t('kernel.providers.solver.add') }}</JecButton>
          </div>
        </section>
      </template>

      <section aria-labelledby="targets" class="flex flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h2 id="targets" class="m-0 text-section-title font-semibold">{{ t('certs.add.targets') }}</h2>
          <RouterLink to="/targets" class="text-small">{{ t('certs.add.manage_targets') }}</RouterLink>
        </div>
        <p v-if="targets.isSuccess.value && !targets.data.value?.length" class="m-0 text-ink-muted">{{ t('certs.add.no_targets') }}</p>
        <label v-for="p in targets.data.value ?? []" :key="p.id" class="flex min-h-11 items-center gap-2.5">
          <input v-model="chosenTargets" type="checkbox" :value="p.id" class="size-4 flex-none accent-brand" />
          <span class="min-w-0 [overflow-wrap:anywhere]">
            <span class="font-semibold" translate="no">{{ p.name }}</span>
            <span class="ml-1.5 text-small text-ink-muted" :translate="typeLabel(p, targetTypes).brand ? 'no' : undefined">
              {{ typeLabel(p, targetTypes).text }}
            </span>
          </span>
        </label>
        <p v-if="badTargets" class="m-0 text-small text-danger-ink">{{ t(badTargets) }}</p>
      </section>

      <section aria-labelledby="renewal" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
        <h2 id="renewal" class="m-0 text-section-title font-semibold">{{ t(uploading || requesting ? 'certs.add.reminder' : 'certs.add.renewal') }}</h2>
        <JecField
          id="renewBeforeDays"
          :label="uploading || requesting ? 'certs.settings.days' : 'certs.add.renew_days'"
          :hint="uploading || requesting ? 'certs.settings.days_help' : 'certs.add.renew_days_help'"
          :error="fieldError('renewBeforeDays')"
        >
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
                :invalid="!!fieldError('renewBeforeDays')"
              />
            </span>
            <span>{{ t('certs.settings.days_unit') }}</span>
          </span>
        </JecField>
      </section>
    </form>

    <section
      aria-labelledby="plan"
      class="flex w-full min-w-0 flex-[1_1_300px] flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4 sm:sticky sm:top-6"
    >
      <h2 id="plan" class="m-0 text-section-title font-semibold">{{ t('certs.add.plan') }}</h2>
      <ol class="m-0 flex list-none flex-col gap-2 p-0">
        <li v-for="(step, i) in plan" :key="i" class="grid grid-cols-[22px_minmax(0,1fr)] gap-2 text-small">
          <data :value="i + 1" class="grid size-5 place-items-center rounded-sm bg-surface-sunken text-caption font-semibold text-ink-muted tabular-nums">
            {{ i + 1 }}
          </data>
          <span class="[overflow-wrap:anywhere]">{{ step }}</span>
        </li>
      </ol>
      <JecButton
        type="submit"
        form="add-form"
        variant="primary"
        :busy="mutation.isPending.value || adding || importer.isPending.value"
        :disabled="source === undefined || (bulk && !uploading && !batch.length) || (uploading && importing && !importFiles.length)"
      >
        <template v-if="uploading && importing"><Upload class="size-4" aria-hidden="true" />{{ t('certs.import.submit', { count: importFiles.length }) }}</template>
        <template v-else-if="uploading"><Save class="size-4" aria-hidden="true" />{{ t('certs.action.save') }}</template>
        <template v-else-if="requesting"><FilePlus class="size-4" aria-hidden="true" />{{ t('certs.request.generate') }}</template>
        <template v-else-if="bulk"><ShieldCheck class="size-4" aria-hidden="true" />{{ t('certs.add.bulk_submit', { count: batch.length }) }}</template>
        <template v-else><ShieldCheck class="size-4" aria-hidden="true" />{{ t('certs.action.issue') }}</template>
      </JecButton>
      <JecButton to="/certificates">{{ t('kernel.action.cancel') }}</JecButton>
    </section>
  </div>
</template>
