<script setup lang="ts">
import { useT } from '../i18n'
import JecMark from './JecMark.vue'

const { t } = useT()

// A hotbar under the form, a picture only (docs/design/boards/Login): a few
// slots with their durability bars, then empty ones. Nine slots do not fit
// a phone, which goes without.
const hotbar = [{ pct: 84 }, { pct: 65 }, { pct: 12, warn: true }, { pct: 92 }, { pct: 48 }, null, null, null, null]
</script>

<template>
  <div class="flex min-h-dvh flex-col items-center justify-center gap-7 px-4 py-8">
    <main class="flex w-full max-w-[360px] flex-col gap-5">
      <div class="flex flex-col items-center gap-2.5 text-center">
        <JecMark :size="48" />
        <h1 class="m-0 text-page-title font-semibold">{{ t('kernel.app.name') }}</h1>
        <p class="m-0 text-ink-muted">{{ t('kernel.app.tagline') }}</p>
      </div>
      <slot />
      <div class="flex flex-wrap items-center justify-center gap-x-4 gap-y-1 text-center text-small text-ink-muted">
        <slot name="footer" />
      </div>
    </main>
    <div class="hidden gap-1 rounded-md border border-line bg-surface-raised p-1 sm:flex" aria-hidden="true">
      <span
        v-for="(s, i) in hotbar"
        :key="i"
        :class="['relative size-12 flex-none rounded-sm', s ? 'bg-surface-sunken shadow-slot' : 'shadow-[inset_0_0_0_1px_var(--line-strong)]']"
      >
        <span v-if="s" class="absolute right-1.5 bottom-[5px] left-1.5 h-1 bg-bar-track p-px shadow-[0_0_0_1px_var(--bar-edge)]">
          <span :class="['block h-full', s.warn ? 'bg-warn-fill' : 'bg-ok-fill']" :style="{ width: `${s.pct}%` }" />
        </span>
      </span>
    </div>
  </div>
</template>
