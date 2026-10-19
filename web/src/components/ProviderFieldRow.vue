<script setup lang="ts">
import type { ProviderField } from '../api/providers'
import { locales, useT, type MessageKey } from '../i18n'
import JecInput from './JecInput.vue'
import JecTextarea from './JecTextarea.vue'

// One setting of a provider's form. A field with a label is JEC's own; one
// without is a third party's setting, shown by its key with the third
// party's own description. A choice that other fields depend on, as the
// ways of signing in, shows its few options side by side.
const props = defineProps<{
  idPrefix: string
  field: ProviderField
  error?: MessageKey
  /** The instance being edited holds this secret already. */
  saved?: boolean
  /** Shows a select's options as buttons side by side. */
  choice?: boolean
}>()
const value = defineModel<string>({ required: true })

const { t } = useT()
const id = `${props.idPrefix}-${props.field.key}`
const types: Partial<Record<ProviderField['kind'], string>> = { url: 'url', email: 'email', number: 'number' }
const inputType = props.field.secret ? 'password' : (types[props.field.kind] ?? 'text')
</script>

<template>
  <div class="grid gap-1">
    <label v-if="field.kind === 'switch'" class="flex items-center gap-2 text-small font-semibold">
      <input
        :id="id"
        type="checkbox"
        class="size-4 accent-brand"
        :checked="value === 'true'"
        @change="value = ($event.target as HTMLInputElement).checked ? 'true' : 'false'"
      />
      <template v-if="field.label">{{ t(field.label as MessageKey) }}</template>
      <code v-else class="font-mono">{{ field.key }}</code>
    </label>
    <template v-else-if="choice">
      <span :id="`${id}-label`" class="text-small font-semibold">{{ t(field.label as MessageKey) }}</span>
      <div :id="id" role="radiogroup" :aria-labelledby="`${id}-label`" class="flex gap-0.5 rounded-md bg-surface-sunken p-0.5">
        <label
          v-for="o in field.options"
          :key="o.value"
          :class="[
            'flex min-h-8 min-w-0 flex-1 cursor-pointer items-center justify-center rounded-sm px-2 py-1 text-center text-small font-semibold max-sm:min-h-10',
            'has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-1 has-[:focus-visible]:outline-[color:var(--focus-ring)]',
            value === o.value ? 'bg-surface-raised text-ink shadow-[0_0_0_1px_var(--line-strong)]' : 'text-ink-muted hover:text-ink',
          ]"
        >
          <input type="radio" class="sr-only" :name="id" :value="o.value" :checked="value === o.value" @change="value = o.value" />
          {{ t(o.label as MessageKey) }}
        </label>
      </div>
    </template>
    <template v-else>
      <label :for="id" class="text-small font-semibold">
        <template v-if="field.label">{{ t(field.label as MessageKey) }}</template>
        <code v-else class="font-mono [overflow-wrap:anywhere]">{{ field.key }}</code>
      </label>
      <select
        v-if="field.kind === 'select'"
        :id="id"
        v-model="value"
        :aria-invalid="!!error || undefined"
        class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink aria-invalid:border-danger-ink max-sm:h-11"
      >
        <option v-for="o in field.options" :key="o.value" :value="o.value">{{ t(o.label as MessageKey) }}</option>
      </select>
      <!-- Each language by its own name. -->
      <select
        v-else-if="field.kind === 'locale'"
        :id="id"
        v-model="value"
        :aria-invalid="!!error || undefined"
        class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink aria-invalid:border-danger-ink max-sm:h-11"
      >
        <option v-for="l in locales" :key="l.tag" :value="l.tag" :lang="l.tag">{{ l.name }}</option>
      </select>
      <JecTextarea
        v-else-if="field.kind === 'multiline'"
        :id="id"
        v-model="value"
        :invalid="!!error"
        :placeholder="saved ? t('kernel.providers.secret_saved') : undefined"
      />
      <JecInput
        v-else
        :id="id"
        v-model="value"
        :type="inputType"
        :inputmode="field.kind === 'number' ? 'numeric' : undefined"
        :mono="!field.label"
        :invalid="!!error"
        :placeholder="saved ? t('kernel.providers.secret_saved') : undefined"
        :autocomplete="field.secret ? 'new-password' : 'off'"
        :required="field.required"
      />
    </template>
    <p v-if="error" class="m-0 text-small text-danger-ink">{{ t(error) }}</p>
    <p v-else-if="field.help" class="m-0 text-caption leading-[18px] text-ink-muted">{{ t(field.help as MessageKey) }}</p>
    <p v-else-if="field.doc" lang="en" class="m-0 text-caption leading-[18px] text-ink-muted [overflow-wrap:anywhere]">{{ field.doc }}</p>
  </div>
</template>
