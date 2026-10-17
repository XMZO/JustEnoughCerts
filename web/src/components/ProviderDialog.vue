<script setup lang="ts">
import { ArrowLeft, Save } from '@lucide/vue'
import { computed, ref, useId, watch } from 'vue'

import { revealError, useApiError } from '../api/errors'
import {
  useCreateProvider,
  useProviderType,
  useProviderTypes,
  useUpdateProvider,
  type Provider,
  type ProviderKind,
  type ProviderTypeSummary,
} from '../api/providers'
import { useT, type MessageKey } from '../i18n'
import { initialValues, settings, typeName } from '../providers/fields'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import ProviderForm from './ProviderForm.vue'
import SearchBox from './SearchBox.vue'

// Adding or editing a provider (docs/design/boards/TargetsAdd): on the
// left the types to choose from, searchable, on the right the form the
// chosen type's fields make. A kind with one type skips the choice; on
// phones the choice and the form take turns.
const props = defineProps<{
  kind: ProviderKind
  title: string
  /** The instance to edit; without one, a new instance is added. */
  instance?: Provider
}>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ saved: [provider: Provider] }>()

const { t, locale } = useT()
// Several dialogs may be on a page at once, as for adding and editing.
const uid = useId()
const types = useProviderTypes(() => props.kind)
const picked = ref<string>()
const type = useProviderType(picked)

// The groups lego's DNS providers come in, as tags.
const tags = [
  { id: '', label: 'kernel.providers.tag.all' },
  { id: 'china', label: 'kernel.providers.tag.china' },
  { id: 'popular', label: 'kernel.providers.tag.popular' },
  { id: 'self_hosted', label: 'kernel.providers.tag.self_hosted' },
] as const
const tag = ref('')
const query = ref('')
const label = (s: ProviderTypeSummary) => {
  const n = typeName(s)
  return n.brand ?? (n.key ? t(n.key) : s.id)
}
const choices = computed(() => {
  const q = query.value.trim().toLowerCase()
  return (types.data.value ?? [])
    .filter((s) => !tag.value || s.tags?.includes(tag.value))
    .filter((s) => !q || label(s).toLowerCase().includes(q) || s.id.toLowerCase().includes(q))
    .sort((a, b) => label(a).localeCompare(label(b)))
})
const single = computed(() => !props.instance && types.data.value?.length === 1)
const hasTags = computed(() => (types.data.value ?? []).some((s) => s.tags?.length))

const name = ref('')
const values = ref<Record<string, string>>({})
const create = useCreateProvider()
const update = useUpdateProvider()
const mutation = computed(() => (props.instance ? update : create))
const errors = useApiError(computed(() => mutation.value.error.value))
const form = ref<HTMLFormElement>()

// Opening starts over: the instance's type, the only type, or none yet.
watch(
  [open, () => types.data.value],
  ([now]) => {
    if (!now) return
    if (props.instance) picked.value = props.instance.type
    else if (single.value) picked.value = types.data.value?.[0]?.id
  },
  { immediate: true },
)
watch(open, (now) => {
  if (now) return
  picked.value = undefined
  query.value = ''
  tag.value = ''
  create.reset()
  update.reset()
})
// A type's form starts from the instance's settings, or the defaults.
watch(
  () => type.data.value,
  (full) => {
    if (!full || full.id !== picked.value) return
    values.value = initialValues(full, props.instance, locale.value)
    const n = typeName(full)
    name.value = props.instance?.name ?? n.brand ?? (n.key ? t(n.key) : full.id)
  },
  { immediate: true },
)

async function submit() {
  const full = type.data.value
  if (!full) return
  const body = { name: name.value, ...settings(full, values.value) }
  let saved: Provider
  try {
    saved = props.instance
      ? await update.mutateAsync({ id: props.instance.id, ...body })
      : await create.mutateAsync({ type: full.id, ...body })
  } catch {
    await revealError(form.value)
    return
  }
  open.value = false
  emit('saved', saved)
}
</script>

<template>
  <JecDialog v-model:open="open" :title="title">
    <div :class="['flex gap-4 max-sm:flex-col', picked || single || instance ? '' : 'min-h-[min(60dvh,480px)]']">
      <div
        v-if="!instance && !single"
        :class="['flex w-full flex-none flex-col gap-2 sm:w-[220px]', picked ? 'max-sm:hidden' : '']"
        role="group"
        :aria-label="t('kernel.providers.choose')"
      >
        <SearchBox v-model="query" label="kernel.providers.search" />
        <div v-if="hasTags" class="flex flex-wrap gap-1.5">
          <button
            v-for="g in tags"
            :key="g.id"
            type="button"
            :aria-pressed="tag === g.id ? 'true' : 'false'"
            :class="[
              'inline-flex h-[30px] cursor-pointer items-center rounded-md border px-2.5 text-small font-semibold whitespace-nowrap text-ink max-sm:h-9',
              tag === g.id ? 'border-brand bg-brand-soft' : 'border-line-strong bg-surface-raised hover:bg-surface-sunken',
            ]"
            @click="tag = g.id"
          >
            {{ t(g.label) }}
          </button>
        </div>
        <div role="listbox" :aria-label="t('kernel.providers.choose')" class="flex max-h-[min(50dvh,420px)] flex-col overflow-y-auto rounded-md border border-line">
          <button
            v-for="s in choices"
            :key="s.id"
            type="button"
            role="option"
            :aria-selected="picked === s.id ? 'true' : 'false'"
            :class="[
              'min-h-9 cursor-pointer border-0 border-b border-line px-3 py-1.5 text-left last:border-b-0 max-sm:min-h-11',
              picked === s.id ? 'bg-brand-soft font-semibold text-ink' : 'bg-transparent text-ink hover:bg-surface-sunken',
            ]"
            :translate="typeName(s).brand ? 'no' : undefined"
            @click="picked = s.id"
          >
            {{ label(s) }}
          </button>
          <p v-if="types.isSuccess.value && !choices.length" class="m-0 p-3 text-small text-ink-muted">{{ t('kernel.providers.no_match') }}</p>
        </div>
        <p v-if="types.data.value" class="m-0 text-caption text-ink-muted">
          {{ t('kernel.providers.count', { count: types.data.value.length }) }}
        </p>
      </div>

      <form
        :id="`${uid}-form`"
        ref="form"
        :class="['flex min-w-0 flex-1 flex-col gap-3', !picked && !single && !instance ? 'max-sm:hidden' : '']"
        novalidate
        @submit.prevent="submit"
      >
        <template v-if="type.data.value && type.data.value.id === picked">
          <button
            v-if="!instance && !single"
            type="button"
            class="flex min-h-11 cursor-pointer items-center gap-1.5 self-start border-0 bg-transparent p-0 text-small font-semibold text-brand sm:hidden"
            @click="picked = undefined"
          >
            <ArrowLeft class="size-4" aria-hidden="true" />{{ t('kernel.providers.choose_other') }}
          </button>
          <div class="flex flex-col gap-0.5">
            <h3 class="m-0 text-heading font-semibold" :translate="type.data.value.brand ? 'no' : undefined">{{ label(type.data.value) }}</h3>
            <p v-if="type.data.value.description" class="m-0 text-small text-ink-muted">{{ t(type.data.value.description as MessageKey) }}</p>
            <p v-else-if="type.data.value.doc" lang="en" class="m-0 text-small text-ink-muted">{{ type.data.value.doc }}</p>
          </div>
          <JecAlert v-if="errors.alert.value" :code="errors.alert.value.code" :params="errors.alert.value.params" />
          <ProviderForm
            v-model:name="name"
            v-model:values="values"
            :id-prefix="uid"
            :type="type.data.value"
            :errors="errors.fields.value"
            :saved="instance?.secretKeys"
          />
        </template>
        <p v-else-if="!picked" class="m-0 text-ink-muted max-sm:hidden">{{ t('kernel.providers.pick_first') }}</p>
      </form>
    </div>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" :form="`${uid}-form`" variant="primary" :busy="mutation.isPending.value" :disabled="!type.data.value || type.data.value.id !== picked">
        <Save class="size-4" aria-hidden="true" />{{ t('kernel.action.save') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
