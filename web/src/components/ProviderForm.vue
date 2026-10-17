<script setup lang="ts">
import { computed } from 'vue'

import type { ProviderField, ProviderType } from '../api/providers'
import { useT, type MessageKey } from '../i18n'
import { applies } from '../providers/fields'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'
import ProviderFieldRow from './ProviderFieldRow.vue'

// The settings of a provider, built from the fields its type declares
// (PLAN.md 5.5), so that a new provider needs no code here. The fields
// that apply to the values so far are shown; advanced ones come last,
// behind a disclosure.
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

const { t } = useT()

const shown = computed(() => props.type.fields.filter((f) => applies(f, values.value, props.type.fields)))
const main = computed(() => shown.value.filter((f) => !f.advanced))
const advanced = computed(() => shown.value.filter((f) => f.advanced))

const error = (f: ProviderField) => props.errors[`${f.secret ? 'secrets' : 'config'}.${f.key}`]
const saved = (f: ProviderField) => !!f.secret && (props.saved ?? []).includes(f.key)

function set(key: string, value: string) {
  values.value = { ...values.value, [key]: value }
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <JecField :id="`${idPrefix}-name`" label="kernel.providers.field.name" hint="kernel.providers.field.name_help" :error="errors.name">
      <JecInput :id="`${idPrefix}-name`" v-model="name" :invalid="!!errors.name" autocomplete="off" required />
    </JecField>
    <ProviderFieldRow
      v-for="f in main"
      :key="f.key"
      :id-prefix="idPrefix"
      :field="f"
      :error="error(f)"
      :saved="saved(f)"
      :model-value="values[f.key] ?? ''"
      @update:model-value="(v: string) => set(f.key, v)"
    />
    <details v-if="advanced.length" class="rounded-md border border-line px-3 py-2" :open="advanced.some((f) => !!error(f))">
      <summary class="cursor-pointer text-small font-semibold">{{ t('kernel.providers.advanced', { count: advanced.length }) }}</summary>
      <div class="mt-3 flex flex-col gap-3">
        <ProviderFieldRow
          v-for="f in advanced"
          :key="f.key"
          :id-prefix="idPrefix"
          :field="f"
          :error="error(f)"
          :saved="saved(f)"
          :model-value="values[f.key] ?? ''"
          @update:model-value="(v: string) => set(f.key, v)"
        />
      </div>
    </details>
  </div>
</template>
