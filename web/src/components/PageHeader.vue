<script setup lang="ts">
import { ArrowLeft, ChevronRight } from '@lucide/vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

import { useT, type MessageKey } from '../i18n'
import JecMark from './JecMark.vue'

// The title of a page. On phones it is the sticky top bar, with icon
// buttons in the "phone" slot; from 640px up it heads the page, with the
// breadcrumb, a subtitle and the buttons in the "actions" slot.
defineProps<{
  title: string
  /** The title is data, such as a domain, rather than a translated text. */
  data?: boolean
  subtitle?: string
  /** The way back: the breadcrumb, and the back button on phones. */
  back?: { to: RouteLocationRaw; label: MessageKey }
}>()

const { t } = useT()
</script>

<template>
  <header
    class="sticky top-0 z-10 flex h-14 items-center gap-2.5 border-b border-line bg-surface-raised pr-2 pl-4 sm:hidden"
    :class="back ? 'pl-1' : ''"
  >
    <RouterLink
      v-if="back"
      :to="back.to"
      :aria-label="t(back.label)"
      class="grid size-11 flex-none place-items-center rounded-md text-ink hover:bg-surface-sunken"
    >
      <ArrowLeft class="size-5" aria-hidden="true" />
    </RouterLink>
    <JecMark v-else :size="32" />
    <h1 class="m-0 min-w-0 flex-1 truncate text-section-title font-semibold" :translate="data ? 'no' : undefined" :title="title">
      {{ title }}
    </h1>
    <slot name="phone" />
  </header>

  <div class="hidden flex-col gap-4 sm:flex">
    <nav v-if="back" :aria-label="t('kernel.shell.breadcrumb')" class="flex min-w-0 items-center gap-1 text-small text-ink-muted">
      <RouterLink :to="back.to" class="no-underline">{{ t(back.label) }}</RouterLink>
      <ChevronRight class="size-4 flex-none" aria-hidden="true" />
      <span aria-current="page" class="truncate" :translate="data ? 'no' : undefined" :title="title">{{ title }}</span>
    </nav>
    <header class="flex flex-wrap items-center justify-between gap-x-4 gap-y-3">
      <div class="flex min-w-0 flex-[1_1_280px] flex-col gap-0.5">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
          <h1 class="m-0 text-page-title font-semibold [overflow-wrap:anywhere]" :translate="data ? 'no' : undefined">{{ title }}</h1>
          <slot name="badge" />
        </div>
        <p v-if="subtitle" class="m-0 text-ink-muted">{{ subtitle }}</p>
        <slot name="subtitle" />
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <slot name="actions" />
      </div>
    </header>
  </div>
</template>
