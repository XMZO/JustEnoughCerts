<script setup lang="ts">
import { ChevronLeft, ChevronRight } from '@lucide/vue'
import { computed } from 'vue'

import { useT } from '../i18n'

// The pages of a long list: the one before and after, and the numbers of
// the first, the last and those around the one shown, gaps between. Phones
// get the one before and after, and where they are.
const page = defineModel<number>({ required: true })
const props = defineProps<{ pages: number }>()

const { t } = useT()

// The numbers to show, with 0 for a gap.
const numbers = computed(() => {
  const n = props.pages
  const near = new Set([1, n, page.value - 1, page.value, page.value + 1].filter((p) => p >= 1 && p <= n))
  const out: number[] = []
  for (const p of [...near].sort((a, b) => a - b)) {
    if (out.length && p - out[out.length - 1]! > 1) out.push(0)
    out.push(p)
  }
  return out
})

function go(p: number) {
  page.value = Math.min(Math.max(1, p), props.pages)
}

const step = 'inline-flex h-9 min-w-9 cursor-pointer items-center justify-center gap-1 rounded-md border px-2.5 text-small font-semibold text-ink disabled:cursor-not-allowed disabled:opacity-45 max-sm:h-11'
const plain = 'border-line-strong bg-surface-raised hover:bg-surface-sunken'
</script>

<template>
  <nav v-if="pages > 1" :aria-label="t('kernel.pager.label')" class="flex flex-wrap items-center justify-center gap-1.5">
    <button type="button" :class="[step, plain]" :disabled="page <= 1" @click="go(page - 1)">
      <ChevronLeft class="size-4" aria-hidden="true" />{{ t('kernel.pager.prev') }}
    </button>
    <template v-for="(p, i) in numbers" :key="p || `gap-${i}`">
      <span v-if="!p" class="px-1 text-ink-muted before:content-['…'] max-sm:hidden" aria-hidden="true" />
      <button
        v-else
        type="button"
        :aria-current="p === page ? 'page' : undefined"
        :aria-label="t('kernel.pager.page', { page: p })"
        :class="[step, 'tabular-nums max-sm:hidden', p === page ? 'border-brand bg-brand-soft' : plain]"
        @click="go(p)"
      >
        <data :value="p">{{ p }}</data>
      </button>
    </template>
    <span class="px-2 text-small text-ink-muted tabular-nums sm:hidden">{{ t('kernel.pager.of', { page, pages }) }}</span>
    <button type="button" :class="[step, plain]" :disabled="page >= pages" @click="go(page + 1)">
      {{ t('kernel.pager.next') }}<ChevronRight class="size-4" aria-hidden="true" />
    </button>
  </nav>
</template>
