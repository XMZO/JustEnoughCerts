<script setup lang="ts">
import { Plus, Save, ShieldCheck } from '@lucide/vue'
import { computed, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { useAddCertificate, useIssueCertificate, type KeyType } from '../api/certs'
import { revealError, useApiError } from '../api/errors'
import { useProviders, useProviderTypes, type Provider } from '../api/providers'
import { keyText } from '../certs/life'
import DomainInput from '../components/DomainInput.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import PageHeader from '../components/PageHeader.vue'
import PemFields from '../components/PemFields.vue'
import { useT, type MessageKey } from '../i18n'
import { typeName } from '../providers/fields'

// Adding a certificate: one page, no wizard (docs/design/boards/AddCert).
// The source is an upload or a configured source, such as an ACME account;
// one that proves control of the domains asks for the DNS provider to do
// it with. The plan on the side says what JEC will do.
const { t } = useT()
const router = useRouter()

const sources = useProviders('source')
const sourceTypes = useProviderTypes('source')
const solvers = useProviders('solver')
const solverTypes = useProviderTypes('solver')

// The choice of source: the first configured one until someone picks.
const picked = ref<'upload' | number>()
const source = computed<'upload' | number | undefined>(() => {
  if (picked.value !== undefined) return picked.value
  if (!sources.isSuccess.value) return undefined
  return sources.data.value?.[0]?.id ?? 'upload'
})
const provider = computed(() => (typeof source.value === 'number' ? sources.data.value?.find((p) => p.id === source.value) : undefined))
const needsSolver = computed(() => !!sourceTypes.data.value?.find((s) => s.id === provider.value?.type)?.needsSolver)

const typeLabel = (p: Provider, types: typeof sourceTypes) => {
  const n = typeName(types.data.value?.find((s) => s.id === p.type))
  return { brand: n.brand, text: n.key ? t(n.key) : (n.brand ?? '') }
}

// Upload.
const add = useAddCertificate()
const pem = ref({ certificate: '', privateKey: '' })

// Issue.
const issue = useIssueCertificate()
const domains = ref<string[]>([])
const pickedSolver = ref<number>()
const solver = computed(() => pickedSolver.value ?? solvers.data.value?.[0]?.id)
const solverName = computed(() => solvers.data.value?.find((s) => s.id === solver.value)?.name ?? '')
const keyTypes: KeyType[] = ['rsa-2048', 'ecdsa-p256', 'ecdsa-p384', 'rsa-3072', 'rsa-4096']
const keyType = ref<KeyType>('rsa-2048')
const keyLabel = (k: string) => {
  const text = keyText(k)
  return t(text.key, text.params)
}

const days = ref('30')
const renewDays = computed(() => Number(days.value) || 30)

const uploading = computed(() => source.value === 'upload')
const mutation = computed(() => (uploading.value ? add : issue))
const errors = useApiError(computed(() => mutation.value.error.value))
const badDomains = computed(() =>
  Object.keys(errors.fields.value)
    .map((f) => /^domains\[(\d+)\]$/.exec(f)?.[1])
    .filter((i): i is string => i !== undefined)
    .map(Number),
)

const plan = computed(() => {
  if (uploading.value) {
    return [t('certs.plan.check'), t('certs.plan.store'), t('certs.plan.remind', { days: renewDays.value })]
  }
  const steps = [t('certs.plan.key', { key: keyLabel(keyType.value) }), t('certs.plan.order', { source: provider.value?.name ?? '' })]
  if (needsSolver.value) steps.push(t('certs.plan.dns', { dns: solverName.value }))
  steps.push(t('certs.plan.download'), t('certs.plan.renew', { days: renewDays.value }))
  return steps
})

const form = ref<HTMLFormElement>()

async function submit() {
  if (uploading.value) {
    let created
    try {
      created = await add.mutateAsync({
        certificate: pem.value.certificate,
        privateKey: pem.value.privateKey || undefined,
        renewBeforeDays: Number(days.value) || undefined,
      })
    } catch {
      await revealError(form.value) // shown by the fields and the alert
      return
    }
    await router.replace(`/certificates/${created.id}`)
    return
  }
  if (typeof source.value !== 'number') return
  let created
  try {
    created = await issue.mutateAsync({
      domains: domains.value,
      sourceProvider: source.value,
      solverProvider: needsSolver.value ? solver.value : undefined,
      keyType: keyType.value,
      renewBeforeDays: Number(days.value) || undefined,
    })
  } catch {
    await revealError(form.value)
    return
  }
  await router.replace(created.issuance ? `/certificates/${created.id}/issuances/${created.issuance.id}` : `/certificates/${created.id}`)
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
            <span class="truncate text-caption leading-[18px] text-ink-muted" :translate="typeLabel(p, sourceTypes).brand ? 'no' : undefined">
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
          :errors="errors.fields.value"
          :error-params="errors.error.value?.params"
        />
        <JecField v-else-if="provider" id="keyType" label="certs.add.key_type" hint="certs.add.key_type_help" :error="fieldError('keyType')">
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
          <h2 id="domains" class="m-0 text-section-title font-semibold">{{ t('certs.add.domains') }}</h2>
          <div class="grid gap-1.5">
            <label for="domain-input" class="sr-only">{{ t('certs.add.domains') }}</label>
            <DomainInput id="domain-input" v-model="domains" :invalid="badDomains" described-by="domains-help" />
            <p v-if="badDomains.length" id="domains-help" class="m-0 text-small text-danger-ink">{{ t('certs.error.bad_domain') }}</p>
            <p v-else-if="fieldError('domains')" id="domains-help" class="m-0 text-small text-danger-ink">{{ t(fieldError('domains')!) }}</p>
            <p v-else id="domains-help" class="m-0 text-small text-ink-muted">{{ t('certs.add.domains_help') }}</p>
          </div>
        </section>

        <section v-if="needsSolver" aria-labelledby="validation" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
          <h2 id="validation" class="m-0 text-section-title font-semibold">{{ t('certs.add.validation') }}</h2>
          <template v-if="solvers.data.value?.length">
            <JecField id="solver" label="certs.add.dns" hint="certs.add.dns_help" :error="fieldError('solverProvider')">
              <select
                id="solver"
                :value="solver"
                class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink max-sm:h-11 sm:max-w-96"
                @change="pickedSolver = Number(($event.target as HTMLSelectElement).value)"
              >
                <option v-for="s in solvers.data.value" :key="s.id" :value="s.id" translate="no">
                  {{ s.name }} · {{ typeLabel(s, solverTypes).text }}
                </option>
              </select>
            </JecField>
            <RouterLink to="/dns" class="self-start text-small">{{ t('certs.add.manage_dns') }}</RouterLink>
          </template>
          <div v-else class="flex flex-col items-start gap-2">
            <p :class="['m-0', fieldError('solverProvider') ? 'text-danger-ink' : 'text-ink-muted']">{{ t('certs.add.no_dns') }}</p>
            <JecButton to="/dns" size="sm"><Plus class="size-4" aria-hidden="true" />{{ t('kernel.providers.solver.add') }}</JecButton>
          </div>
        </section>
      </template>

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
      <JecButton type="submit" form="add-form" variant="primary" :busy="mutation.isPending.value" :disabled="source === undefined">
        <template v-if="uploading"><Save class="size-4" aria-hidden="true" />{{ t('certs.action.save') }}</template>
        <template v-else><ShieldCheck class="size-4" aria-hidden="true" />{{ t('certs.action.issue') }}</template>
      </JecButton>
      <JecButton to="/certificates">{{ t('kernel.action.cancel') }}</JecButton>
    </section>
  </div>
</template>
