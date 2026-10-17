<script setup lang="ts">
import { Pencil, Plus, Trash2 } from '@lucide/vue'
import { useQueries } from '@tanstack/vue-query'
import { computed, ref } from 'vue'

import { useCertificates } from '../api/certs'
import { api, unwrap } from '../api/client'
import { useApiError } from '../api/errors'
import { useDeleteProvider, useProviders, useProviderTypes, type Provider } from '../api/providers'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import PageHeader from '../components/PageHeader.vue'
import ProviderDialog from '../components/ProviderDialog.vue'
import { useT } from '../i18n'
import { kindPages, summary, typeName, type PageKind } from '../providers/fields'

// The configured providers of one kind (docs/design/boards/Providers):
// certificate sources, or DNS providers for the domain validation. Each
// card says what it is, what it is set to and how many certificates use it.
const props = defineProps<{ kind: PageKind }>()

const { t } = useT()
const page = computed(() => kindPages[props.kind])
const list = useProviders(() => props.kind)
const types = useProviderTypes(() => props.kind)
const certificates = useCertificates()
const loadError = useApiError(computed(() => list.error.value))

// The full types of the instances, for the settings their cards show.
const typeIds = computed(() => [...new Set((list.data.value ?? []).map((p) => p.type))])
const fullTypes = useQueries({
  queries: computed(() =>
    typeIds.value.map((id) => ({
      queryKey: ['providers', 'type', id] as const,
      queryFn: () => unwrap(api.GET('/providers/types/{id}', { params: { path: { id } } })),
      staleTime: 5 * 60_000,
    })),
  ),
})
const fullType = (id: string) => fullTypes.value.find((q) => q.data?.id === id)?.data

const cards = computed(() =>
  (list.data.value ?? []).map((p) => {
    const summaryType = types.data.value?.find((s) => s.id === p.type)
    const name = typeName(summaryType)
    const uses = (certificates.data.value ?? []).filter((c) => c.sourceProvider === p.id || c.solverProvider === p.id).length
    return {
      provider: p,
      typeBrand: name.brand,
      typeLabel: name.key ? t(name.key) : (name.brand ?? (summaryType ? '' : t('kernel.providers.type_gone'))),
      shown: summary(fullType(p.type), p),
      secrets: p.secretKeys.length,
      uses,
    }
  }),
)

const adding = ref(false)
const editing = ref<Provider>()
const editOpen = computed({
  get: () => editing.value !== undefined,
  set: (open) => {
    if (!open) editing.value = undefined
  },
})

const deleting = ref<Provider>()
const deleteOpen = computed({
  get: () => deleting.value !== undefined,
  set: (open) => {
    if (!open) deleting.value = undefined
  },
})
const remove = useDeleteProvider()
const removeError = useApiError(remove.error)
function startDelete(p: Provider) {
  remove.reset()
  deleting.value = p
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
  <PageHeader :title="t(page.title)" :subtitle="t(page.intro)">
    <template #phone>
      <button
        type="button"
        :aria-label="t(page.add)"
        class="grid size-11 cursor-pointer place-items-center rounded-md border-0 bg-brand text-on-brand hover:bg-brand-hover"
        @click="adding = true"
      >
        <Plus class="size-4" aria-hidden="true" />
      </button>
    </template>
    <template #actions>
      <JecButton variant="primary" @click="adding = true"><Plus class="size-4" aria-hidden="true" />{{ t(page.add) }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t(page.intro) }}</p>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />

    <section v-if="list.isSuccess.value && !cards.length" class="flex flex-col items-start gap-3 rounded-lg border border-line bg-surface-raised p-4">
      <p class="m-0 text-ink-muted">{{ t(page.empty) }}</p>
      <JecButton variant="primary" @click="adding = true"><Plus class="size-4" aria-hidden="true" />{{ t(page.add) }}</JecButton>
    </section>

    <div v-else-if="cards.length" class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] gap-3">
      <article v-for="c in cards" :key="c.provider.id" class="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
        <div class="flex min-w-0 flex-col gap-0.5">
          <h2 class="m-0 truncate text-heading font-semibold" translate="no" :title="c.provider.name">{{ c.provider.name }}</h2>
          <span class="truncate text-small text-ink-muted" :translate="c.typeBrand ? 'no' : undefined" :title="c.typeLabel">{{ c.typeLabel }}</span>
        </div>
        <dl v-if="c.shown.length" class="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-small">
          <template v-for="s in c.shown" :key="s.key">
            <dt class="whitespace-nowrap text-ink-muted">
              <template v-if="s.label">{{ t(s.label) }}</template>
              <code v-else class="font-mono">{{ s.key }}</code>
            </dt>
            <dd v-if="s.option" class="m-0 truncate" :title="t(s.option)">{{ t(s.option) }}</dd>
            <dd v-else class="m-0 truncate" translate="no" :title="s.value">{{ s.value }}</dd>
          </template>
        </dl>
        <p class="m-0 text-small text-ink-muted">
          {{ c.secrets ? t('kernel.providers.secrets_saved', { count: c.secrets }) : t('kernel.providers.no_secrets') }}
        </p>
        <p class="m-0 text-small text-ink-muted">{{ t('kernel.providers.uses', { count: c.uses }) }}</p>
        <div class="mt-auto flex flex-wrap gap-2 pt-1">
          <JecButton size="sm" @click="editing = c.provider"><Pencil class="size-4" aria-hidden="true" />{{ t('kernel.action.edit') }}</JecButton>
          <JecButton size="sm" variant="ghost" @click="startDelete(c.provider)"><Trash2 class="size-4" aria-hidden="true" />{{ t('kernel.action.delete') }}</JecButton>
        </div>
      </article>
    </div>
  </div>

  <ProviderDialog v-model:open="adding" :kind="kind" :title="t(page.add)" />
  <ProviderDialog v-if="editing" v-model:open="editOpen" :kind="kind" :instance="editing" :title="t('kernel.providers.edit', { name: editing.name })" />

  <JecDialog v-model:open="deleteOpen" :title="t('kernel.providers.delete.title', { name: deleting?.name ?? '' })" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('kernel.providers.delete.body') }}</p>
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
