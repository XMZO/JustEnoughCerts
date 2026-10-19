<script setup lang="ts">
import { computed } from 'vue'

import { hint, useCredentialKinds, useCredentials } from '../api/credentials'
import type { ProviderField, ProviderType } from '../api/providers'
import { useT, type MessageKey } from '../i18n'
import { applies, choice, credentialChoice } from '../providers/fields'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'
import ProviderFieldRow from './ProviderFieldRow.vue'

// The settings of a provider, built from the fields its type declares
// (PLAN.md 5.5), so that a new provider needs no code here. The fields
// that apply to the values so far are shown, side by side as room allows;
// advanced ones come last, behind a disclosure. A type that takes a credential (PLAN.md 6.1) has
// the choice of one first: a new one of the keys typed in, one there is,
// whose fields then go, or keys of the provider's own.
const props = defineProps<{
  /** Makes the IDs of the inputs unique on the page. */
  idPrefix: string
  type: ProviderType
  /** Field errors as useApiError names them: name, config.<key>, secrets.<key>. */
  errors: Record<string, MessageKey>
  /** The secrets the instance being edited holds already. */
  saved?: string[]
}>()
const name = defineModel<string>('name', { required: true })
const values = defineModel<Record<string, string>>('values', { required: true })
/** For a type that takes a credential: credentialChoice's new, own, or the ID of one. */
const credential = defineModel<string>('credential', { default: '' })

const { t } = useT()

const takes = computed(() => props.type.credentialKind ?? '')
const kinds = useCredentialKinds()
const all = useCredentials()
const kind = computed(() => kinds.data.value?.find((k) => k.id === takes.value))
const choices = computed(() =>
  (all.data.value ?? [])
    .filter((c) => c.kind === takes.value)
    .map((c) => ({ value: String(c.id), name: c.name, hint: hint(kind.value, c) })),
)
const credentialHint = computed<MessageKey>(() => {
  switch (credential.value) {
    case credentialChoice.new:
      return 'kernel.providers.credential.help_new'
    case credentialChoice.own:
      return 'kernel.providers.credential.help_own'
  }
  return 'kernel.providers.credential.help_shared'
})
// The fields a credential fills are its business once one is chosen.
const filled = computed(() =>
  takes.value && credential.value !== credentialChoice.new && credential.value !== credentialChoice.own ? (props.type.credentialFields ?? []) : [],
)

const shown = computed(() => props.type.fields.filter((f) => !filled.value.includes(f.key) && applies(f, values.value, props.type.fields)))
const main = computed(() => shown.value.filter((f) => !f.advanced))
const advanced = computed(() => shown.value.filter((f) => f.advanced))

const error = (f: ProviderField) => props.errors[`${f.secret ? 'secrets' : 'config'}.${f.key}`]
// A field of several lines, such as a key, takes a row of its own, as a
// choice does, so that its options read in one line.
const wide = (f: ProviderField) => (f.kind === 'multiline' || choice(f, props.type.fields) ? 'col-span-full' : '')
// As many columns as fit: two in a wide dialog, one on a phone.
const grid = 'grid grid-cols-[repeat(auto-fill,minmax(min(100%,15rem),1fr))] items-start gap-x-4 gap-y-2.5 sm:gap-y-3'
const saved = (f: ProviderField) => !!f.secret && (props.saved ?? []).includes(f.key)

function set(key: string, value: string) {
  values.value = { ...values.value, [key]: value }
}
</script>

<template>
  <div :class="grid">
    <JecField :id="`${idPrefix}-name`" label="kernel.providers.field.name" hint="kernel.providers.field.name_help" :error="errors.name">
      <JecInput :id="`${idPrefix}-name`" v-model="name" :invalid="!!errors.name" autocomplete="off" required />
    </JecField>
    <JecField v-if="takes" :id="`${idPrefix}-credential`" label="kernel.providers.credential.label" :hint="credentialHint" :error="errors.credentialId">
      <select
        :id="`${idPrefix}-credential`"
        v-model="credential"
        :aria-invalid="!!errors.credentialId || undefined"
        class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink aria-invalid:border-danger-ink max-sm:h-11"
      >
        <option :value="credentialChoice.new">{{ t('kernel.providers.credential.new', { kind: kind ? t(kind.name as MessageKey) : takes }) }}</option>
        <option v-for="c in choices" :key="c.value" :value="c.value" translate="no">
          {{ c.hint ? t('kernel.providers.credential.option', { name: c.name, hint: c.hint }) : c.name }}
        </option>
        <option :value="credentialChoice.own">{{ t('kernel.providers.credential.own') }}</option>
      </select>
    </JecField>
    <!-- A field comes in gently as the form changes to show it; not those
         behind the disclosure, whose animations would wait while it is shut. -->
    <ProviderFieldRow
      v-for="f in main"
      :key="f.key"
      :class="[wide(f), 'animate-fade-in']"
      :id-prefix="idPrefix"
      :field="f"
      :choice="choice(f, type.fields)"
      :error="error(f)"
      :saved="saved(f)"
      :model-value="values[f.key] ?? ''"
      @update:model-value="(v: string) => set(f.key, v)"
    />
    <details v-if="advanced.length" class="col-span-full rounded-md border border-line px-3 py-2" :open="advanced.some((f) => !!error(f))">
      <summary class="cursor-pointer text-small font-semibold">{{ t('kernel.providers.advanced', { count: advanced.length }) }}</summary>
      <div :class="['mt-3', grid]">
        <ProviderFieldRow
          v-for="f in advanced"
          :key="f.key"
          :class="wide(f)"
          :id-prefix="idPrefix"
          :field="f"
          :choice="choice(f, type.fields)"
          :error="error(f)"
          :saved="saved(f)"
          :model-value="values[f.key] ?? ''"
          @update:model-value="(v: string) => set(f.key, v)"
        />
      </div>
    </details>
  </div>
</template>
