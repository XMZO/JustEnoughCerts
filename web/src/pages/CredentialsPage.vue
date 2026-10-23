<script setup lang="ts">
import { KeyRound, Pencil, Plus, Trash2, TriangleAlert } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { hint, useCredentialKinds, useCredentials, useDeleteCredential, type Credential } from '../api/credentials'
import { useApiError } from '../api/errors'
import CredentialDialog from '../components/CredentialDialog.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import JecPager from '../components/JecPager.vue'
import PageHeader from '../components/PageHeader.vue'
import RowMenu, { type MenuItem } from '../components/RowMenu.vue'
import SearchBox from '../components/SearchBox.vue'
import { keepPlace, useFitRows } from '../fit'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import { fromMore } from '../shell'

// The credentials (docs/design/boards/Credentials, PLAN.md 6.1): the keys
// that several providers share. Each card tells what it is, shows the part
// of it that tells it apart, which providers use it and when one last did.
// On phones the actions are in the card's menu, and only those in trouble
// are cards: the others are rows, a page at a time, as the providers'.
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
      usedText: c.usedBy.length ? t('kernel.credentials.used_by', { names: intl.list(c.usedBy.map((u) => u.name)) }) : t('kernel.credentials.unused'),
      menu: [
        { key: 'edit', label: t('kernel.action.edit'), icon: Pencil, run: () => (editing.value = c) },
        { key: 'delete', label: t('kernel.action.delete'), icon: Trash2, danger: true, run: () => startDelete(c) },
      ] satisfies MenuItem[],
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

// On phones: those no type can use stay cards, first; the others are rows
// of two lines, tapped to edit, a page of them filling the screen (the page
// in the address), with a search once there are more than a few.
const route = useRoute()
const router = useRouter()
const query = ref('')
const found = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return cards.value
  return cards.value.filter((c) => [c.credential.name, c.kindLabel, ...c.users].some((v) => v.toLowerCase().includes(q)))
})
const troubled = computed(() => new Set(found.value.filter((c) => !c.credential.available).map((c) => c.credential.id)))
const calm = computed(() => found.value.filter((c) => c.credential.available))
const rows = ref<HTMLElement>()
const after = ref<HTMLElement>()
const size = useFitRows(rows, {
  row: () => 57,
  // Under the rows: the pager, what the page says last, its margin and the
  // tab bar.
  below: () => (after.value?.offsetHeight ?? 0) + 150,
  min: 4,
  again: () => cards.value,
})
const pageNo = computed({
  get: () => Math.max(1, Number(route.query.page) || 1),
  set: (p: number) => {
    void router.replace({ query: { ...route.query, page: p > 1 ? String(p) : undefined } })
    window.scrollTo({ top: 0 })
  },
})
watch(query, () => {
  if (pageNo.value > 1) pageNo.value = 1
})
watch(size, (n, o) => {
  if (o && n !== o && pageNo.value > 1) pageNo.value = keepPlace(pageNo.value, o, n)
})
const pages = computed(() => Math.max(1, Math.ceil(calm.value.length / Math.max(1, size.value))))
// Past the last page, as after some were deleted: the last one.
const at = computed({
  get: () => Math.min(pageNo.value, pages.value),
  set: (p: number) => (pageNo.value = p),
})
const shownRows = computed(() => calm.value.slice((at.value - 1) * size.value, at.value * size.value))

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

    <SearchBox v-if="cards.length > 6" v-model="query" label="kernel.credentials.search" class="sm:hidden" />
    <p v-if="query && !found.length" class="m-0 text-ink-muted sm:hidden">{{ t('kernel.providers.no_match') }}</p>

    <div
      v-if="cards.length"
      :class="['grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] gap-3', troubled.size ? '' : 'max-sm:hidden']"
    >
      <article
        v-for="c in cards"
        :key="c.credential.id"
        :class="['flex min-w-0 flex-col gap-1.5 rounded-lg border border-line bg-surface-raised p-3 sm:gap-2 sm:p-4', troubled.has(c.credential.id) ? '' : 'max-sm:hidden']"
      >
        <div class="flex min-w-0 items-start gap-2">
          <KeyRound class="mt-1 size-4 flex-none text-ink-muted" aria-hidden="true" />
          <div class="flex min-w-0 flex-1 flex-col gap-0.5">
            <h2 class="m-0 truncate text-heading font-semibold" translate="no" :title="c.credential.name">{{ c.credential.name }}</h2>
            <span class="flex min-w-0 flex-wrap gap-x-1.5 text-small text-ink-muted">
              <span class="truncate" :title="c.kindLabel">{{ c.kindLabel }}</span>
              <code v-if="c.hint" class="font-mono" translate="no">{{ c.hint }}</code>
            </span>
          </div>
          <div class="flex-none sm:hidden">
            <RowMenu :label="t('kernel.action.more_for', { name: c.credential.name })" :items="c.menu" />
          </div>
        </div>
        <p v-if="!c.credential.available" class="m-0 flex items-start gap-1.5 text-small text-warn-ink">
          <TriangleAlert class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ t('kernel.credentials.unavailable') }}
        </p>
        <!-- Who uses it and when one last did, on one line while it fits. -->
        <p class="m-0 text-small [overflow-wrap:anywhere]">
          <span class="mr-1.5">{{ c.usedText }}</span>
          <span class="inline-block text-ink-muted before:mr-1.5 before:content-['·']">
            <template v-if="c.credential.usedAt">
              {{ t('kernel.credentials.last_used') }} <time :datetime="c.credential.usedAt">{{ intl.dateTime(c.credential.usedAt) }}</time>
            </template>
            <template v-else>{{ t('kernel.credentials.never_used') }}</template>
          </span>
        </p>
        <div class="mt-auto flex flex-wrap gap-2 pt-1 max-sm:hidden">
          <JecButton size="sm" @click="editing = c.credential"><Pencil class="size-4" aria-hidden="true" />{{ t('kernel.action.edit') }}</JecButton>
          <JecButton size="sm" variant="ghost" @click="startDelete(c.credential)"><Trash2 class="size-4" aria-hidden="true" />{{ t('kernel.action.delete') }}</JecButton>
        </div>
      </article>
    </div>

    <!-- Phones: the others, a row each, tapped to edit. -->
    <div v-if="calm.length" class="flex flex-col gap-3 sm:hidden">
      <ul ref="rows" class="m-0 list-none rounded-lg border border-line bg-surface-raised p-0">
        <li v-for="c in shownRows" :key="c.credential.id" data-row class="flex min-w-0 items-center gap-1 border-b border-line px-3 last:border-b-0">
          <button
            type="button"
            class="flex min-h-14 min-w-0 flex-1 cursor-pointer flex-col justify-center gap-0.5 border-0 bg-transparent px-0 py-1.5 text-left text-ink"
            @click="editing = c.credential"
          >
            <span class="flex min-w-0 items-baseline gap-1.5">
              <span class="truncate font-semibold" translate="no" :title="c.credential.name">{{ c.credential.name }}</span>
              <code v-if="c.hint" class="flex-none font-mono text-small text-ink-muted" translate="no">{{ c.hint }}</code>
            </span>
            <!-- Each part cut short with all of it in a tooltip. -->
            <span class="flex min-w-0 text-small whitespace-nowrap text-ink-muted">
              <span class="min-w-0 shrink-[2] truncate" :title="c.kindLabel">{{ c.kindLabel }}</span>
              <span class="min-w-0 truncate before:mx-1.5 before:content-['·']" :title="c.usedText">{{ c.usedText }}</span>
            </span>
          </button>
          <RowMenu :label="t('kernel.action.more_for', { name: c.credential.name })" :items="c.menu" />
        </li>
      </ul>
      <JecPager v-model="at" :pages="pages" />
    </div>

    <p ref="after" class="m-0 text-small text-ink-muted">{{ t('kernel.credentials.foot') }}</p>
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
