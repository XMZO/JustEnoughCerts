<script setup lang="ts">
import { LogOut } from '@lucide/vue'
import { useRouter } from 'vue-router'

import { useSession, useSignOut } from '../api/account'
import JecButton from '../components/JecButton.vue'
import JecMark from '../components/JecMark.vue'
import { useT } from '../i18n'

const { t } = useT()
const router = useRouter()
const session = useSession()
const signOut = useSignOut()

async function leave() {
  try {
    await signOut.mutateAsync()
  } finally {
    await router.replace('/login')
  }
}
</script>

<template>
  <div class="min-h-dvh">
    <header class="flex items-center justify-between gap-3 border-b border-line bg-surface-raised px-4 py-2.5">
      <div class="flex items-center gap-2.5">
        <JecMark :size="32" />
        <span class="font-semibold">{{ t('kernel.app.name') }}</span>
      </div>
      <div class="flex min-w-0 items-center gap-2 text-small text-ink-muted">
        <span v-if="session.data.value" class="truncate">{{ t('kernel.home.signed_in', { username: session.data.value.username }) }}</span>
        <JecButton variant="ghost" size="sm" :busy="signOut.isPending.value" @click="leave">
          <LogOut class="size-4" aria-hidden="true" />{{ t('kernel.home.sign_out') }}
        </JecButton>
      </div>
    </header>
    <main class="mx-auto flex max-w-[1120px] flex-col gap-4 px-4 py-6 sm:px-8">
      <h1 class="m-0 text-page-title font-semibold">{{ t('kernel.home.title') }}</h1>
      <p class="m-0 rounded-lg border border-line bg-surface-raised p-4 text-ink-muted">{{ t('kernel.home.empty') }}</p>
    </main>
  </div>
</template>
