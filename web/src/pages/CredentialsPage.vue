<script setup lang="ts">
import { KeyRound, Pencil, Plus, Trash2, TriangleAlert } from '@lucide/vue'
import { computed, ref } from 'vue'

import { hint, useCredentialKinds, useCredentials, useDeleteCredential, type Credential } from '../api/credentials'
import { useApiError } from '../api/errors'
import CredentialDialog from '../components/CredentialDialog.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import PageHeader from '../components/PageHeader.vue'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import { fromMore } from '../shell'

// The credentials (docs/design/boards/Credentials, PLAN.md 6.1): the keys
// that several providers share. Each card tells what it is, shows the part
// of it that tells it apart, which providers use it and when one last did.
const { t } = useT()
const intl = useIntl()
const list = useCredentials()
const kinds = useCredentialKinds()
const loadError = useApiError(computed(() => list.error.value))

const cards = computed(() =>
  (list.data.value ?? []).map((c) => {
    const kind = kinds.data.value?.find((k) => k.id === c.kind)
    return {
      credential: c,
      kindLabel: kind ? t(kind.name as MessageKey) : c.kind,
      hint: hint(kind, c),
      users: c.usedBy.map((u) => u.name),
    }
  }),
)

const adding = ref(false)
const editing = ref<Credential>()
const editOpen = computed({
  get: () => editing.value !== undefined,
  set: (open) => {
    if (!open) editing.value = undefined
  },
})

const deleting = ref<Credential>()
const deleteOpen = computed({
  get: () => deleting.value !== undefined,
  set: (open) => {
    if (!open) deleting.value = undefined
  },
})
const remove = useDeleteCredential()
const removeError = useApiError(remove.error)
function startDelete(c: Credential) {
  remove.reset()
  deleting.value = c
}
async function confirmDelete() {
  if (!deleting.value) return
  try {
    await remove.mutateAsync(deleting.value.id)
  } catch {
    return // shown in the dialog
  }
  deleting.value = undefined
}
</script>

<template>
  <PageHeader :title="t('kernel.credentials.title')" :subtitle="t('kernel.credentials.intro')" :phone-back="fromMore">
    <template #phone>
      <button
        type="button"
        :aria-label="t('kernel.credentials.add')"
        class="grid size-11 cursor-pointer place-items-center rounded-md border-0 bg-brand text-on-brand hover:bg-brand-hover"
        @click="adding = true"
      >
        <Plus class="size-4" aria-hidden="true" />
      </button>
    </template>
    <template #actions>
      <JecButton variant="primary" @click="adding = true"><Plus class="size-4" aria-hidden="true" />{{ t('kernel.credentials.add') }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t('kernel.credentials.intro') }}</p>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />

    <section v-if="list.isSuccess.value && !cards.length" class="flex flex-col items-start gap-3 rounded-lg border border-line bg-surface-raised p-4">
      <p class="m-0 text-ink-muted">{{ t('kernel.credentials.empty') }}</p>
      <JecButton variant="primary" @click="adding = true"><Plus class="size-4" aria-hidden="true" />{{ t('kernel.credentials.add') }}</JecButton>
    </section>

    <div v-else-if="cards.length" class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] gap-3">
      <article v-for="c in cards" :key="c.credential.id" class="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
        <div class="flex min-w-0 items-start gap-2">
          <KeyRound class="mt-1 size-4 flex-none text-ink-muted" aria-hidden="true" />
          <div class="flex min-w-0 flex-col gap-0.5">
            <h2 class="m-0 truncate text-heading font-semibold" translate="no" :title="c.credential.name">{{ c.credential.name }}</h2>
            <span class="flex min-w-0 flex-wrap gap-x-1.5 text-small text-ink-muted">
              <span class="truncate" :title="c.kindLabel">{{ c.kindLabel }}</span>
              <code v-if="c.hint" class="font-mono" translate="no">{{ c.hint }}</code>
            </span>
          </div>
        </div>
        <p v-if="!c.credential.available" class="m-0 flex items-start gap-1.5 text-small text-warn-ink">
          <TriangleAlert class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ t('kernel.credentials.unavailable') }}
        </p>
        <p class="m-0 text-small [overflow-wrap:anywhere]">
          {{ c.users.length ? t('kernel.credentials.used_by', { names: intl.list(c.users) }) : t('kernel.credentials.unused') }}
        </p>
        <p class="m-0 text-small text-ink-muted">
          <template v-if="c.credential.usedAt">
            {{ t('kernel.credentials.last_used') }} <time :datetime="c.credential.usedAt">{{ intl.dateTime(c.credential.usedAt) }}</time>
          </template>
          <template v-else>{{ t('kernel.credentials.never_used') }}</template>
        </p>
        <div class="mt-auto flex flex-wrap gap-2 pt-1">
          <JecButton size="sm" @click="editing = c.credential"><Pencil class="size-4" aria-hidden="true" />{{ t('kernel.action.edit') }}</JecButton>
          <JecButton size="sm" variant="ghost" @click="startDelete(c.credential)"><Trash2 class="size-4" aria-hidden="true" />{{ t('kernel.action.delete') }}</JecButton>
        </div>
      </article>
    </div>

    <p class="m-0 text-small text-ink-muted">{{ t('kernel.credentials.foot') }}</p>
  </div>

  <CredentialDialog v-model:open="adding" :title="t('kernel.credentials.add')" />
  <CredentialDialog v-if="editing" v-model:open="editOpen" :credential="editing" :title="t('kernel.credentials.edit', { name: editing.name })" />

  <JecDialog v-model:open="deleteOpen" :title="t('kernel.credentials.delete.title', { name: deleting?.name ?? '' })" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('kernel.credentials.delete.body') }}</p>
      <JecAlert v-if="removeError.error.value" :code="removeError.error.value.code" :params="removeError.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="deleteOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="danger" :busy="remove.isPending.value" @click="confirmDelete">
        <Trash2 class="size-4" aria-hidden="true" />{{ t('kernel.action.delete') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
