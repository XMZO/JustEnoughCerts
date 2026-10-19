<script setup lang="ts">
import { ArrowLeft, PlugZap, Save } from '@lucide/vue'
import { computed, ref, useId, watch } from 'vue'

import { ApiError, networkError } from '../api/client'
import { revealError, useApiError } from '../api/errors'
import {
  useCreateProvider,
  useProviderTest,
  useProviderType,
  useProviderTypes,
  useUpdateProvider,
  type Provider,
  type ProviderKind,
  type ProviderTypeSummary,
} from '../api/providers'
import { useT, type MessageKey, type Params } from '../i18n'
import { credentialChoice, credentialSettings, initialCredential, initialValues, settings, typeName } from '../providers/fields'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import ProviderForm from './ProviderForm.vue'
import SearchBox from './SearchBox.vue'
import TypeTags from './TypeTags.vue'

// Adding or editing a provider (docs/design/boards/TargetsAdd): on the
// left the types to choose from, searchable, on the right the form the
// chosen type's fields make. A kind with one type skips the choice; on
// phones the choice and the form take turns. A testable type can be saved
// and tested at once, the dialog staying open with what the test said:
// some tests keep what they find, as a registration, so only a saved
// provider is tested. The dialog keeps its size as types and fields
// change; the type before stays, dimmed, until the next one loads, which
// then comes in gently.
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

// The groups types come in, as tags: lego's DNS providers by region and
// use, solvers over HTTP apart, and those that add records of any type, as
// some sources ask. Only the groups of this kind show.
const allTags = [
  { id: '', label: 'kernel.providers.tag.all' },
  { id: 'china', label: 'kernel.providers.tag.china' },
  { id: 'popular', label: 'kernel.providers.tag.popular' },
  { id: 'self_hosted', label: 'kernel.providers.tag.self_hosted' },
  { id: 'http', label: 'kernel.providers.tag.http' },
  { id: 'records', label: 'kernel.providers.tag.records' },
  { id: 'plugin', label: 'kernel.providers.tag.plugin' },
] as const
const tags = computed(() => allTags.filter((g) => !g.id || types.data.value?.some((s) => s.tags?.includes(g.id))))
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
// A name typed in stays, even when the type's form comes after it.
const nameTyped = ref(false)
const nameModel = computed({
  get: () => name.value,
  set: (v: string) => {
    name.value = v
    nameTyped.value = true
  },
})
// The type whose form is set up, once each time the dialog opens.
const formFor = ref<string>()
const values = ref<Record<string, string>>({})
const credential = ref('')
const create = useCreateProvider()
const update = useUpdateProvider()
// The provider as saved so far: the one being edited, or the one this
// dialog added once it was saved and tested.
const current = ref<Provider>()
const mutation = computed(() => (current.value ? update : create))
const test = useProviderTest()
// What the last test said, and whether saving is for a test.
const tested = ref<ApiError | string>()
const testing = ref(false)
const errors = useApiError(computed(() => mutation.value.error.value))
const form = ref<HTMLFormElement>()

// Opening starts over: the instance's type, the only type, or none yet.
watch(
  [open, () => types.data.value],
  ([now]) => {
    if (!now) return
    current.value = props.instance
    tested.value = undefined
    if (props.instance) picked.value = props.instance.type
    else if (single.value) picked.value = types.data.value?.[0]?.id
  },
  { immediate: true },
)
watch(open, (now) => {
  if (now) return
  picked.value = undefined
  formFor.value = undefined
  nameTyped.value = false
  query.value = ''
  tag.value = ''
  create.reset()
  update.reset()
})
// A type's form starts from the instance's settings, or the defaults,
// once: what comes later, such as the type fetched anew, leaves what was
// typed alone.
watch(
  [() => type.data.value, picked, open],
  ([full]) => {
    if (!open.value || !full || full.id !== picked.value || formFor.value === full.id) return
    formFor.value = full.id
    values.value = initialValues(full, props.instance, locale.value)
    credential.value = initialCredential(full, props.instance)
    const n = typeName(full)
    if (!nameTyped.value) name.value = props.instance?.name ?? n.brand ?? (n.key ? t(n.key) : full.id)
  },
  { immediate: true },
)

async function submit(andTest = false) {
  const full = type.data.value
  if (!full) return
  tested.value = undefined
  testing.value = andTest
  // A credential chosen fills its fields, which the form then hides.
  const shared = full.credentialKind && credential.value !== credentialChoice.new && credential.value !== credentialChoice.own
  const body = {
    name: name.value,
    ...settings(full, values.value, shared ? (full.credentialFields ?? []) : []),
    ...credentialSettings(full, credential.value, current.value),
  }
  let saved: Provider
  try {
    saved = current.value ? await update.mutateAsync({ id: current.value.id, ...body }) : await create.mutateAsync({ type: full.id, ...body })
  } catch {
    testing.value = false
    await revealError(form.value)
    return
  }
  const added = !current.value
  current.value = saved
  if (added) emit('saved', saved)
  if (!andTest) {
    open.value = false
    return
  }
  // The keys typed in are saved now: the form shows them so, and a
  // credential made of them is the one chosen.
  values.value = initialValues(full, saved, locale.value)
  credential.value = initialCredential(full, saved)
  try {
    const { message } = await test.mutateAsync(saved.id)
    tested.value = t(message.key as MessageKey, (message.params ?? {}) as Params)
  } catch (err) {
    tested.value = err instanceof ApiError ? err : networkError
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <JecDialog v-model:open="open" :title="title" wide steady>
    <div class="flex gap-4 max-sm:flex-col sm:h-full">
      <div
        v-if="!current && !single"
        :class="['flex w-full flex-none flex-col gap-2 sm:min-h-0 sm:w-[220px]', picked ? 'max-sm:hidden' : '']"
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
        <div role="listbox" :aria-label="t('kernel.providers.choose')" class="flex min-h-0 flex-col overflow-y-auto rounded-md border border-line sm:flex-1">
          <button
            v-for="s in choices"
            :key="s.id"
            type="button"
            role="option"
            :aria-selected="picked === s.id ? 'true' : 'false'"
            :class="[
              'flex min-h-9 cursor-pointer items-center gap-1.5 border-0 border-b border-line px-3 py-1.5 text-left last:border-b-0 max-sm:min-h-11',
              picked === s.id ? 'bg-brand-soft font-semibold text-ink' : 'bg-transparent text-ink hover:bg-surface-sunken',
            ]"
            @click="picked = s.id"
          >
            <span class="min-w-0 [overflow-wrap:anywhere]" :translate="typeName(s).brand ? 'no' : undefined">{{ label(s) }}</span>
            <TypeTags :type="s" />
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
        :class="['flex min-w-0 flex-1 flex-col sm:min-h-0 sm:overflow-y-auto sm:p-1', !picked && !single && !current ? 'max-sm:hidden' : '']"
        novalidate
        @submit.prevent="submit()"
      >
        <div
          v-if="type.data.value && picked"
          :key="type.data.value.id"
          :inert="type.data.value.id !== picked"
          :class="['flex animate-fade-in flex-col gap-2.5 transition-opacity sm:gap-3', type.data.value.id !== picked ? 'opacity-50' : '']"
        >
          <button
            v-if="!current && !single"
            type="button"
            class="flex min-h-11 cursor-pointer items-center gap-1.5 self-start border-0 bg-transparent p-0 text-small font-semibold text-brand sm:hidden"
            @click="picked = undefined"
          >
            <ArrowLeft class="size-4" aria-hidden="true" />{{ t('kernel.providers.choose_other') }}
          </button>
          <div class="flex flex-col gap-0.5">
            <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
              <h3 class="m-0 text-heading font-semibold" :translate="type.data.value.brand ? 'no' : undefined">{{ label(type.data.value) }}</h3>
              <TypeTags :type="type.data.value" />
            </div>
            <!-- A description may set out steps, a line each. -->
            <p v-if="type.data.value.description" class="m-0 text-caption whitespace-pre-line text-ink-muted sm:text-small">
              {{ t(type.data.value.description as MessageKey) }}
            </p>
            <p v-else-if="type.data.value.doc" lang="en" class="m-0 text-small text-ink-muted">{{ type.data.value.doc }}</p>
            <p v-if="type.data.value.untested" class="m-0 text-caption text-ink-muted">{{ t('kernel.providers.untested_help') }}</p>
          </div>
          <JecAlert v-if="errors.alert.value" :code="errors.alert.value.code" :params="errors.alert.value.params" />
          <ProviderForm
            v-model:name="nameModel"
            v-model:values="values"
            v-model:credential="credential"
            :id-prefix="uid"
            :type="type.data.value"
            :errors="errors.fields.value"
            :saved="current?.secretKeys"
          />
          <p v-if="typeof tested === 'string'" role="status" class="m-0 text-small text-ok-ink [overflow-wrap:anywhere]">{{ tested }}</p>
          <JecAlert v-else-if="tested" :code="tested.code" :params="tested.params" />
        </div>
        <p v-else-if="!picked" class="m-0 text-ink-muted max-sm:hidden">{{ t('kernel.providers.pick_first') }}</p>
      </form>
    </div>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton v-if="type.data.value?.testable && type.data.value.id === picked" :busy="testing" @click="submit(true)">
        <PlugZap class="size-4" aria-hidden="true" />{{ t('kernel.providers.save_test') }}
      </JecButton>
      <JecButton
        type="submit"
        :form="`${uid}-form`"
        variant="primary"
        :busy="mutation.isPending.value && !testing"
        :disabled="!type.data.value || type.data.value.id !== picked"
      >
        <Save class="size-4" aria-hidden="true" />{{ t('kernel.action.save') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
