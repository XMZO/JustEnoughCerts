<script setup lang="ts">
import PageHeader from '../components/PageHeader.vue'
import TransparencySection from '../components/TransparencySection.vue'
import { useT } from '../i18n'
import { fromMore } from '../shell'

// Certificate transparency (module watch), a page of its own: what it has
// to do with the sites watched is only the module.
const { t } = useT()
</script>

<template>
  <PageHeader :title="t('watch.ct.title')" :phone-back="fromMore" />
  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <TransparencySection />
  </div>
</template>
