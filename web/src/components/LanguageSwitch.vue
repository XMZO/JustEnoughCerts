<script setup lang="ts">
import { Languages } from '@lucide/vue'
import { computed } from 'vue'

import { locales, setLocale, useT } from '../i18n'

const { t, locale } = useT()

// Each click moves on to the next language, written in itself.
const next = computed(() => {
  const i = locales.findIndex((l) => l.tag === locale.value)
  return locales[(i + 1) % locales.length] ?? locales[0]
})
</script>

<template>
  <button
    type="button"
    class="inline-flex items-center gap-1 border-0 bg-transparent p-0 text-brand hover:text-brand-hover"
    :title="t('kernel.app.language')"
    :lang="next.tag"
    @click="setLocale(next.tag)"
  >
    <Languages class="size-3.5" aria-hidden="true" />{{ next.name }}
  </button>
</template>
