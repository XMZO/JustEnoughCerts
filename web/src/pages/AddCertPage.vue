<script setup lang="ts">
import { CircleCheck, CircleX, Plus, Save, ShieldCheck, TriangleAlert } from '@lucide/vue'
import { computed, ref, watch, watchEffect } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { ApiError, networkError } from '../api/client'
import { useAddCertificate, useCertSettings, useIssueCertificate, type KeyType } from '../api/certs'
import { revealError, useApiError } from '../api/errors'
import { useProviders, useProviderTypes, useZones, type Provider } from '../api/providers'
import { loadLastChoice, saveLastChoice } from '../certs/lastChoice'
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
// What the last certificate was added with, in this browser: the targets
// start from those still there.
const last = loadLastChoice()
watch(
  () => targets.data.value,
  (list, before) => {
    if (list && !before && last.targets) chosenTargets.value = last.targets.filter((id) => list.some((p) => p.id === id))
  },
  { immediate: true },
)

// The choice of source: the one the last certificate was added with, or
// the first configured one, until someone picks; or an upload when the
// link asks for one, as importing does.
const picked = ref<'upload' | number | undefined>(route.query.source === 'upload' ? 'upload' : undefined)
const source = computed<'upload' | number | undefined>(() => {
  if (picked.value !== undefined) return picked.value
  if (!sources.isSuccess.value) return undefined
  const list = sources.data.value ?? []
  return list.find((p) => p.id === last.source)?.id ?? list[0]?.id ?? 'upload'
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
const takes = (p: Provider) =>
  accepted.value.includes(challengeOf(p)) || (challengeOf(p) === 'dns' && !!solverType(p)?.records && accepted.value.includes('record'))
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

// Issue.
const issue = useIssueCertificate()
const domains = ref<string[]>([])
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
  () => usable.value.find((p) => p.id === (pickedSolver.value ?? last.solver))?.id ?? usable.value[0]?.id,
)
const solverProvider = computed(() => usable.value.find((p) => p.id === solver.value))
// Whether the solver's account holds the zones of the domains, for a type
// that can tell: a domain it cannot prove fails the issuance.
const zoneDomains = computed(() => [...new Set(bulk.value ? batch.value.flat() : domains.value)].slice(0, 50))
const zones = useZones(solver, zoneDomains, () => needsSolver.value && !!solverProvider.value && !!solverType(solverProvider.value)?.zones)
const notHeld = computed(() => (zones.data.value ?? []).filter((z) => z.held === false))
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
  if (!st || defaulted) return
  defaulted = true
  keyType.value = st.keyType
  days.value = String(st.renewBeforeDays)
})

const uploading = computed(() => source.value === 'upload')
const mutation = computed(() => (uploading.value ? add : issue))
const errors = useApiError(computed(() => mutation.value.error.value))
const badTargets = computed(() => Object.entries(errors.fields.value).find(([f]) => f.startsWith('targets'))?.[1])
const badDomains = computed(() =>
  Object.keys(errors.fields.value)
    .map((f) => /^domains\[(\d+)\]$/.exec(f)?.[1])
    .filter((i): i is string => i !== undefined)
    .map(Number),
)

const deploySteps = computed(() =>
  chosenTargets.value.map((id) => t('certs.plan.deploy', { target: targets.data.value?.find((p) => p.id === id)?.name ?? '' })),
)
const plan = computed(() => {
  if (uploading.value) {
    return [t('certs.plan.check'), t('certs.plan.store'), ...deploySteps.value, t('certs.plan.remind', { days: renewDays.value })]
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
    steps.push(challengeOf(solverProvider.value) === 'http' ? t('certs.plan.http', { solver: name }) : t('certs.plan.dns', { dns: name }))
  }
  steps.push(t('certs.plan.download'))
  steps.push(...deploySteps.value, t('certs.plan.renew', { days: renewDays.value, time }))
  return steps
})

const form = ref<HTMLFormElement>()

// Keeps what this certificate is added with, for the next one.
const remember = () =>
  saveLastChoice({ source: typeof source.value === 'number' ? source.value : undefined, solver: needsSolver.value ? solver.value : undefined, targets: chosenTargets.value })

async function submit() {
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
          <RouterLink
            to="/sources"
            class="grid min-h-16 content-start gap-0.5 rounded-md border border-dashed border-line-strong px-3 py-2.5 text-ink no-underline hover:bg-surface-sunken hover:text-ink"
          >
            <span class="flex items-center gap-1.5 text-body font-semibold"><Plus class="size-4" aria-hidden="true" />{{ t('certs.add.new_source') }}</span>
            <span class="text-caption leading-[18px] text-ink-muted">{{ t('certs.add.new_source_desc') }}</span>
          </RouterLink>
        </div>
        <JecAlert v-if="errors.alert.value" :code="errors.alert.value.code" :params="errors.alert.value.params" />
        <PemFields
          v-if="uploading"
          v-model:certificate="pem.certificate"
          v-model:private-key="pem.privateKey"
          v-model:pfx="pem.pfx"
          v-model:pfx-password="pem.pfxPassword"
          :errors="errors.fields.value"
          :error-params="errors.error.value?.params"
        />
        <JecField v-else-if="provider && !ownKey" id="keyType" label="certs.add.key_type" hint="certs.add.key_type_help" :error="fieldError('keyType')">
          <select
            id="keyType"
            v-model="keyType"
            class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink max-sm:h-11 sm:max-w-60"
          >
            <option v-for="k in keyTypes" :key="k" :value="k">{{ keyLabel(k) }}</option>
          </select>
        </JecField>
      </section>

      <template v-if="provider">
        <section aria-labelledby="domains" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 id="domains" class="m-0 text-section-title font-semibold">{{ t('certs.add.domains') }}</h2>
            <div role="group" :aria-label="t('certs.add.bulk_mode')" class="inline-flex gap-0.5 rounded-md border border-line bg-surface-sunken p-0.5">
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
            <p v-if="allHeld" role="status" class="m-0 flex items-start gap-1.5 text-small text-ok-ink">
              <CircleCheck class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ t('certs.add.zones_held') }}
            </p>
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
        <h2 id="renewal" class="m-0 text-section-title font-semibold">{{ t(uploading ? 'certs.add.reminder' : 'certs.add.renewal') }}</h2>
        <JecField
          id="renewBeforeDays"
          :label="uploading ? 'certs.settings.days' : 'certs.add.renew_days'"
          :hint="uploading ? 'certs.settings.days_help' : 'certs.add.renew_days_help'"
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
        :busy="mutation.isPending.value || adding"
        :disabled="source === undefined || (bulk && !uploading && !batch.length)"
      >
        <template v-if="uploading"><Save class="size-4" aria-hidden="true" />{{ t('certs.action.save') }}</template>
        <template v-else-if="bulk"><ShieldCheck class="size-4" aria-hidden="true" />{{ t('certs.add.bulk_submit', { count: batch.length }) }}</template>
        <template v-else><ShieldCheck class="size-4" aria-hidden="true" />{{ t('certs.action.issue') }}</template>
      </JecButton>
      <JecButton to="/certificates">{{ t('kernel.action.cancel') }}</JecButton>
    </section>
  </div>
</template>
