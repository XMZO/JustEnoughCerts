<script setup lang="ts">
import { Languages } from '@lucide/vue'
import { computed } from 'vue'

import { locales, setLocale, useT } from '../i18n'

const { t, locale } = useT()

// It shows the language in use, written in itself; each click moves on to
// the next one.
const index = computed(() => Math.max(0, locales.findIndex((l) => l.tag === locale.value)))
const current = computed(() => locales[index.value] ?? locales[0])
const next = computed(() => locales[(index.value + 1) % locales.length] ?? locales[0])
</script>

<template>
  <button
    type="button"
    class="inline-flex items-center gap-1 border-0 bg-transparent p-0 text-brand hover:text-brand-hover"
    :title="t('kernel.app.language')"
    :lang="current?.tag"
    @click="next && setLocale(next.tag)"
  >
    <Languages class="size-3.5" aria-hidden="true" />{{ current?.name }}
  </button>
</template>
