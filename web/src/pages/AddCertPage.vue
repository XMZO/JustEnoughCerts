<script setup lang="ts">
import { Save } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useAddCertificate } from '../api/certs'
import { revealError, useApiError } from '../api/errors'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import PageHeader from '../components/PageHeader.vue'
import PemFields from '../components/PemFields.vue'
import { useT } from '../i18n'

// Adding a certificate: one page, no wizard (docs/design/boards/AddCert).
// The source cards list every source there is; for now that is the
// upload, and the others come with their modules.
const { t } = useT()
const router = useRouter()
const add = useAddCertificate()
const errors = useApiError(add.error)

const form = ref<HTMLFormElement>()
const pem = ref({ certificate: '', privateKey: '' })
const days = ref('30')
const reminderDays = computed(() => Number(days.value) || 30)
const plan = computed(() => [
  t('certs.plan.check'),
  t('certs.plan.store'),
  t('certs.plan.remind', { days: reminderDays.value }),
])

async function submit() {
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
}
</script>

<template>
  <PageHeader :title="t('certs.action.add')" :subtitle="t('certs.add.intro')" :back="{ to: '/certificates', label: 'certs.list.title' }" />

  <div class="flex flex-wrap items-start gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <form id="add-form" ref="form" class="flex min-w-0 flex-[999_1_560px] flex-col gap-3 sm:gap-4" novalidate @submit.prevent="submit">
      <section aria-labelledby="source" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
        <h2 id="source" class="m-0 text-section-title font-semibold">{{ t('certs.add.source') }}</h2>
        <div role="radiogroup" aria-labelledby="source" class="grid grid-cols-[repeat(auto-fill,minmax(196px,1fr))] gap-2 max-sm:grid-cols-2">
          <button
            type="button"
            role="radio"
            aria-checked="true"
            class="grid min-h-16 cursor-pointer content-start gap-0.5 rounded-md border border-brand bg-brand-soft px-3 py-2.5 text-left text-ink shadow-[inset_0_0_0_1px_var(--brand)]"
          >
            <span class="text-body font-semibold">{{ t('certs.source.upload') }}</span>
            <span class="text-caption leading-[18px] text-ink-muted">{{ t('certs.source.upload_desc') }}</span>
          </button>
        </div>
        <JecAlert v-if="errors.alert.value" :code="errors.alert.value.code" :params="errors.alert.value.params" />
        <PemFields
          v-model:certificate="pem.certificate"
          v-model:private-key="pem.privateKey"
          :errors="errors.fields.value"
          :error-params="errors.error.value?.params"
        />
      </section>

      <section aria-labelledby="reminder" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
        <h2 id="reminder" class="m-0 text-section-title font-semibold">{{ t('certs.add.reminder') }}</h2>
        <JecField id="renewBeforeDays" label="certs.settings.days" hint="certs.settings.days_help" :error="errors.fields.value.renewBeforeDays">
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
                :invalid="!!errors.fields.value.renewBeforeDays"
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
          <span>{{ step }}</span>
        </li>
      </ol>
      <JecButton type="submit" form="add-form" variant="primary" :busy="add.isPending.value">
        <Save class="size-4" aria-hidden="true" />{{ t('certs.action.save') }}
      </JecButton>
      <JecButton to="/certificates">{{ t('kernel.action.cancel') }}</JecButton>
    </section>
  </div>
</template>
