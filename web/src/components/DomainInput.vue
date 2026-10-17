<script setup lang="ts">
import { X } from '@lucide/vue'
import { ref } from 'vue'

import { useT } from '../i18n'

// Domains as chips (docs/design/boards/AddCert): type one and press Enter,
// a comma or a space; a paste may hold several. The first is the primary
// domain. Chips the server found malformed are marked.
const domains = defineModel<string[]>({ required: true })
defineProps<{ id: string; invalid?: number[]; describedBy?: string }>()

const { t } = useT()
const draft = ref('')

// Commas of Chinese text separate domains too: the full-width one and the
// enumeration comma.
const wideCommas = [String.fromCodePoint(0xff0c), String.fromCodePoint(0x3001)]

function split(text: string): string[] {
  for (const c of wideCommas) text = text.replaceAll(c, ',')
  return text
    .split(/[\s,;]+/)
    .map((d) => d.trim())
    .filter(Boolean)
}

function add(text: string) {
  const fresh = [...new Set(split(text))].filter((d) => !domains.value.includes(d))
  if (fresh.length) domains.value = [...domains.value, ...fresh]
}

function commit() {
  add(draft.value)
  draft.value = ''
}

function remove(i: number) {
  domains.value = domains.value.filter((_, j) => j !== i)
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'Enter' || e.key === ',' || e.key === ' ') {
    e.preventDefault()
    commit()
  } else if (e.key === 'Backspace' && !draft.value && domains.value.length) {
    remove(domains.value.length - 1)
  }
}

function onPaste(e: ClipboardEvent) {
  const text = e.clipboardData?.getData('text') ?? ''
  if (split(text).length < 2) return
  e.preventDefault()
  add(draft.value + text)
  draft.value = ''
}
</script>

<template>
  <div
    :class="[
      'flex min-h-11 flex-wrap items-center gap-1.5 rounded-md border bg-surface-raised p-1.5 focus-within:outline-2 focus-within:outline-brand',
      invalid?.length ? 'border-danger-ink' : 'border-line-strong',
    ]"
  >
    <span
      v-for="(d, i) in domains"
      :key="d"
      :class="[
        'inline-flex max-w-full min-w-0 items-center gap-1 rounded-sm py-0.5 pr-0.5 pl-2 text-small font-semibold',
        invalid?.includes(i) ? 'bg-danger-soft text-danger-ink' : i === 0 ? 'bg-brand-soft text-ink' : 'bg-surface-sunken text-ink',
      ]"
    >
      <span class="min-w-0 truncate" translate="no" :title="d">{{ d }}</span>
      <button
        type="button"
        :aria-label="t('certs.domains.remove', { domain: d })"
        :title="t('certs.domains.remove', { domain: d })"
        class="grid size-7 flex-none cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-ink-muted hover:bg-surface-raised hover:text-ink"
        @click="remove(i)"
      >
        <X class="size-3.5" aria-hidden="true" />
      </button>
    </span>
    <input
      :id="id"
      v-model="draft"
      type="text"
      inputmode="url"
      autocapitalize="none"
      autocomplete="off"
      spellcheck="false"
      :placeholder="t('certs.domains.placeholder')"
      :aria-describedby="describedBy"
      :aria-invalid="invalid?.length ? true : undefined"
      class="h-8 min-w-[12ch] flex-1 border-0 bg-transparent px-1.5 text-ink outline-none placeholder:text-ink-muted"
      @keydown="onKey"
      @paste="onPaste"
      @blur="commit"
    />
  </div>
</template>
