<script setup lang="ts">
import { useT } from '../i18n'
import JecMark from './JecMark.vue'

const { t } = useT()
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
  </div>
</template>
