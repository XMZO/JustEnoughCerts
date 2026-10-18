<script setup lang="ts">
import { ArrowLeft, Save } from '@lucide/vue'
import { computed, ref, useId, watch } from 'vue'

import { useCreateCredential, useCredentialKinds, useUpdateCredential, type Credential, type CredentialKind } from '../api/credentials'
import { revealError, useApiError } from '../api/errors'
import { useT, type MessageKey } from '../i18n'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'
import ProviderFieldRow from './ProviderFieldRow.vue'

// Adding or changing a credential (docs/design/boards/Credentials): on the
// left the kinds of credential the providers of the modules that are on
// take, on the right the fields of the kind chosen. A change keeps the
// kind; on phones the choice and the form take turns, as for providers.
const props = defineProps<{
  title: string
  /** The credential to change; without one, a new one is added. */
  credential?: Credential
}>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useT()
const uid = useId()
const kinds = useCredentialKinds()
const picked = ref<string>()
const kind = computed<CredentialKind | undefined>(() => kinds.data.value?.find((k) => k.id === picked.value))

const name = ref('')
const values = ref<Record<string, string>>({})
const create = useCreateCredential()
const update = useUpdateCredential()
const mutation = computed(() => (props.credential ? update : create))
const errors = useApiError(computed(() => mutation.value.error.value))
const form = ref<HTMLFormElement>()

watch(
  [open, () => kinds.data.value],
  ([now]) => {
    if (!now) return
    if (props.credential) picked.value = props.credential.kind
    else if (kinds.data.value?.length === 1) picked.value = kinds.data.value[0]?.id
  },
  { immediate: true },
)
watch(open, (now) => {
  if (now) return
  picked.value = undefined
  create.reset()
  update.reset()
})
// A kind's form starts from the credential's values, or empty.
watch(
  kind,
  (k) => {
    if (!k) return
    const v: Record<string, string> = {}
    for (const f of k.fields) v[f.key] = f.secret ? '' : (props.credential?.config[f.key] ?? '')
    values.value = v
    name.value = props.credential?.name ?? ''
  },
  { immediate: true },
)

const error = (f: CredentialKind['fields'][number]) => errors.fields.value[`${f.secret ? 'secrets' : 'config'}.${f.key}`]
const saved = (f: CredentialKind['fields'][number]) => !!f.secret && (props.credential?.secretKeys ?? []).includes(f.key)

async function submit() {
  const k = kind.value
  if (!k) return
  const config: Record<string, string> = {}
  const secrets: Record<string, string> = {}
  for (const f of k.fields) {
    const v = values.value[f.key] ?? ''
    if (!v.trim()) continue
    if (f.secret) secrets[f.key] = v
    else config[f.key] = v
  }
  try {
    if (props.credential) await update.mutateAsync({ id: props.credential.id, name: name.value, config, secrets })
    else await create.mutateAsync({ kind: k.id, name: name.value, config, secrets })
  } catch {
    await revealError(form.value)
    return
  }
  open.value = false
}
</script>

<template>
  <JecDialog v-model:open="open" :title="title">
    <div class="flex gap-4 max-sm:flex-col">
      <div
        v-if="!credential"
        :class="['flex w-full flex-none flex-col gap-2 sm:w-[220px]', picked ? 'max-sm:hidden' : '']"
        role="group"
        :aria-label="t('kernel.credentials.choose')"
      >
        <div role="listbox" :aria-label="t('kernel.credentials.choose')" class="flex flex-col overflow-y-auto rounded-md border border-line">
          <button
            v-for="k in kinds.data.value ?? []"
            :key="k.id"
            type="button"
            role="option"
            :aria-selected="picked === k.id ? 'true' : 'false'"
            :class="[
              'flex min-h-9 cursor-pointer items-center border-0 border-b border-line px-3 py-1.5 text-left last:border-b-0 max-sm:min-h-11',
              picked === k.id ? 'bg-brand-soft font-semibold text-ink' : 'bg-transparent text-ink hover:bg-surface-sunken',
            ]"
            @click="picked = k.id"
          >
            <span class="min-w-0 [overflow-wrap:anywhere]">{{ t(k.name as MessageKey) }}</span>
          </button>
        </div>
        <p v-if="kinds.isSuccess.value && !kinds.data.value?.length" class="m-0 text-small text-ink-muted">{{ t('kernel.credentials.no_kinds') }}</p>
      </div>

      <form :id="`${uid}-form`" ref="form" :class="['flex min-w-0 flex-1 flex-col gap-3', !picked && !credential ? 'max-sm:hidden' : '']" novalidate @submit.prevent="submit">
        <template v-if="kind">
          <button
            v-if="!credential"
            type="button"
            class="flex min-h-11 cursor-pointer items-center gap-1.5 self-start border-0 bg-transparent p-0 text-small font-semibold text-brand sm:hidden"
            @click="picked = undefined"
          >
            <ArrowLeft class="size-4" aria-hidden="true" />{{ t('kernel.providers.choose_other') }}
          </button>
          <div class="flex flex-col gap-0.5">
            <h3 class="m-0 text-heading font-semibold">{{ t(kind.name as MessageKey) }}</h3>
            <p v-if="kind.description" class="m-0 text-small text-ink-muted">{{ t(kind.description as MessageKey) }}</p>
          </div>
          <JecAlert v-if="errors.alert.value" :code="errors.alert.value.code" :params="errors.alert.value.params" />
          <JecField :id="`${uid}-name`" label="kernel.providers.field.name" hint="kernel.credentials.field.name_help" :error="errors.fields.value.name">
            <JecInput :id="`${uid}-name`" v-model="name" :invalid="!!errors.fields.value.name" autocomplete="off" required />
          </JecField>
          <ProviderFieldRow
            v-for="f in kind.fields"
            :key="f.key"
            :id-prefix="uid"
            :field="f"
            :error="error(f)"
            :saved="saved(f)"
            :model-value="values[f.key] ?? ''"
            @update:model-value="(v: string) => (values = { ...values, [f.key]: v })"
          />
        </template>
        <p v-else-if="!credential" class="m-0 text-ink-muted max-sm:hidden">{{ t('kernel.credentials.pick_first') }}</p>
      </form>
    </div>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" :form="`${uid}-form`" variant="primary" :busy="mutation.isPending.value" :disabled="!kind">
        <Save class="size-4" aria-hidden="true" />{{ t('kernel.action.save') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
