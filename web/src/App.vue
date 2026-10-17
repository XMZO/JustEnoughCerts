<script setup lang="ts">
import { watch } from 'vue'
import { RouterView } from 'vue-router'

import { useT } from './i18n'
import { updateTitle } from './router'

const { locale } = useT()
watch(locale, updateTitle)
</script>

<template>
  <RouterView />
</template>
