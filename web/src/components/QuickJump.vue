<script setup lang="ts">
import { Search } from '@lucide/vue'
import { computed, ref, watch, type Component } from 'vue'
import { useRouter, type RouteLocationRaw } from 'vue-router'

import { useCertificates } from '../api/certs'
import { useProviders } from '../api/providers'
import { matches, searchTerm } from '../certs/search'
import { stateText } from '../certs/life'
import { useT, type MessageKey } from '../i18n'
import { kindPages, type PageKind } from '../providers/fields'
import JecDialog from './JecDialog.vue'

// The quick jump: a search over the certificates, the pages and the
// providers, opened with Ctrl+K (⌘K on a Mac) or its button, that goes to
// the one chosen. The field is a combobox over a listbox, moved through
// with the arrow keys.
export interface Page {
  to: string
  label: MessageKey
  icon: Component
}
const props = defineProps<{ pages: Page[] }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useT()
const router = useRouter()
const certificates = useCertificates()
// The shell mounts the jump when it first opens: only then are the
// providers asked for.
const providers = (Object.keys(kindPages) as PageKind[]).map((kind) => ({ kind, list: useProviders(kind) }))

interface Item {
  id: string
  to: RouteLocationRaw
  name: string
  kind: string
  data: boolean // the name is data, not text of JEC
}
const max = 50
const query = ref('')
const items = computed<Item[]>(() => {
  const q = searchTerm(query.value)
  const out: Item[] = []
  for (const c of certificates.data.value ?? []) {
    if (q && matches(c, q)) {
      out.push({ id: `c${c.id}`, to: `/certificates/${c.id}`, name: c.domains[0] ?? '', kind: t(stateText[c.state]), data: true })
    }
  }
  for (const p of props.pages) {
    const name = t(p.label)
    if (!q || name.toLowerCase().includes(q)) out.push({ id: `p${p.to}`, to: p.to, name, kind: t('kernel.jump.page'), data: false })
  }
  if (q) {
    for (const { kind, list } of providers) {
      for (const p of list.data.value ?? []) {
        if (p.name.toLowerCase().includes(q)) {
          out.push({ id: `${kind}${p.id}`, to: { path: kindPages[kind].path, query: { edit: String(p.id) } }, name: p.name, kind: t(kindPages[kind].title), data: true })
        }
      }
    }
  }
  return out.slice(0, max)
})

const active = ref(0)
watch(items, () => (active.value = 0))
watch(open, (now) => {
  if (now) query.value = ''
})

function move(by: number) {
  if (!items.value.length) return
  active.value = (active.value + by + items.value.length) % items.value.length
  document.getElementById(`jump-${items.value[active.value]!.id}`)?.scrollIntoView({ block: 'nearest' })
}
async function go(item?: Item) {
  if (!item) return
  open.value = false
  await router.push(item.to)
}
</script>

<template>
  <JecDialog v-model:open="open" :title="t('kernel.jump.title')">
    <div class="flex flex-col gap-2">
      <label for="jump" class="sr-only">{{ t('kernel.jump.label') }}</label>
      <div class="relative">
        <Search class="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-ink-muted" aria-hidden="true" />
        <input
          id="jump"
          v-model="query"
          type="text"
          role="combobox"
          aria-expanded="true"
          aria-controls="jump-list"
          aria-autocomplete="list"
          :aria-activedescendant="items[active] ? `jump-${items[active]!.id}` : undefined"
          :placeholder="t('kernel.jump.label')"
          autocomplete="off"
          spellcheck="false"
          class="h-11 w-full rounded-md border border-line-strong bg-surface-raised pr-3 pl-9 text-ink"
          @keydown.down.prevent="move(1)"
          @keydown.up.prevent="move(-1)"
          @keydown.enter.prevent="go(items[active])"
        />
      </div>
      <ul id="jump-list" role="listbox" :aria-label="t('kernel.jump.results')" class="m-0 max-h-[min(60dvh,24rem)] list-none overflow-y-auto p-0">
        <li
          v-for="(item, i) in items"
          :id="`jump-${item.id}`"
          :key="item.id"
          role="option"
          :aria-selected="i === active ? 'true' : 'false'"
          class="flex min-h-11 cursor-pointer items-center gap-3 rounded-md px-3 py-1.5 aria-selected:bg-brand-soft"
          @mousemove="active = i"
          @click="go(item)"
        >
          <span class="min-w-0 flex-1 truncate font-semibold" :translate="item.data ? 'no' : undefined" :title="item.name">{{ item.name }}</span>
          <span class="flex-none text-caption text-ink-muted">{{ item.kind }}</span>
        </li>
      </ul>
      <p v-if="!items.length" role="status" class="m-0 py-2 text-small text-ink-muted [overflow-wrap:anywhere]">{{ t('kernel.jump.none', { query }) }}</p>
    </div>
  </JecDialog>
</template>
