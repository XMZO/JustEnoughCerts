<script setup lang="ts">
import { LogOut } from '@lucide/vue'
import { computed } from 'vue'
import { useRouter } from 'vue-router'

import { useSession, useSignOut } from '../api/account'
import { useManifest } from '../api/manifest'
import JecButton from '../components/JecButton.vue'
import LanguageSwitch from '../components/LanguageSwitch.vue'
import PageHeader from '../components/PageHeader.vue'
import { useT } from '../i18n'

// The phone tab "More": what the sidebar holds on wider screens.
const { t } = useT()
const router = useRouter()
const session = useSession()
const signOut = useSignOut()
const manifest = useManifest()

const signedIn = computed(() => (session.data.value ? t('kernel.shell.signed_in', { username: session.data.value.username }) : ''))

async function leave() {
  try {
    await signOut.mutateAsync()
  } finally {
    await router.replace('/login')
  }
}
</script>

<template>
  <PageHeader :title="t('kernel.shell.more')" />

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <section aria-labelledby="account" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
      <h2 id="account" class="m-0 text-heading font-semibold">{{ t('kernel.more.account') }}</h2>
      <p v-if="signedIn" class="m-0 text-ink-muted">{{ signedIn }}</p>
      <JecButton :busy="signOut.isPending.value" @click="leave"><LogOut class="size-4" aria-hidden="true" />{{ t('kernel.shell.sign_out') }}</JecButton>
    </section>
    <div class="flex flex-wrap items-center justify-between gap-2 px-1 text-small text-ink-muted">
      <LanguageSwitch />
      <span v-if="manifest.data.value">{{ t('kernel.shell.version', { version: manifest.data.value.version }) }}</span>
    </div>
  </div>
</template>
