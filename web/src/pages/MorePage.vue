<script setup lang="ts">
import { Bell, Blocks, ChevronRight, Globe, KeyRound, LogOut, Package, ScrollText, Server, Settings } from '@lucide/vue'
import { computed } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { useSession, useSignOut } from '../api/account'
import { useCredentials } from '../api/credentials'
import { useManifest } from '../api/manifest'
import { useProviders } from '../api/providers'
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

// Each with how many there are of it.
const lists = {
  sources: useProviders('source'),
  solvers: useProviders('solver'),
  targets: useProviders('target'),
  notifiers: useProviders('notifier'),
  credentials: useCredentials(),
}
const config = computed(() => [
  { to: '/sources', label: 'kernel.providers.source.title', icon: Package, count: lists.sources.data.value?.length },
  { to: '/dns', label: 'kernel.providers.solver.title', icon: Globe, count: lists.solvers.data.value?.length },
  { to: '/targets', label: 'kernel.providers.target.title', icon: Server, count: lists.targets.data.value?.length },
  { to: '/notify', label: 'kernel.providers.notifier.title', icon: Bell, count: lists.notifiers.data.value?.length },
  { to: '/credentials', label: 'kernel.credentials.title', icon: KeyRound, count: lists.credentials.data.value?.length },
] as const)

const modulesOn = computed(() => manifest.data.value?.modules.filter((m) => m.required || m.switched).length)
const audited = computed(() => manifest.data.value?.modules.some((m) => m.id === 'audit' && m.enabled) ?? false)

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
    <section aria-labelledby="config" class="overflow-hidden rounded-lg border border-line bg-surface-raised">
      <h2 id="config" class="m-0 px-4 pt-3 pb-1 text-heading font-semibold">{{ t('kernel.shell.config') }}</h2>
      <RouterLink
        v-for="link in config"
        :key="link.to"
        :to="link.to"
        class="flex min-h-12 items-center gap-3 border-t border-line px-4 text-ink no-underline first-of-type:border-t-0 hover:bg-surface-sunken hover:text-ink"
      >
        <component :is="link.icon" class="size-5 flex-none text-ink-muted" aria-hidden="true" />
        <span class="min-w-0 flex-1">{{ t(link.label) }}</span>
        <span v-if="link.count !== undefined" class="text-small text-ink-muted">{{ t('kernel.more.count', { count: link.count }) }}</span>
        <ChevronRight class="size-4 flex-none text-ink-muted" aria-hidden="true" />
      </RouterLink>
    </section>
    <section aria-labelledby="system" class="overflow-hidden rounded-lg border border-line bg-surface-raised">
      <h2 id="system" class="m-0 px-4 pt-3 pb-1 text-heading font-semibold">{{ t('kernel.more.system') }}</h2>
      <RouterLink to="/modules" class="flex min-h-12 items-center gap-3 px-4 text-ink no-underline hover:bg-surface-sunken hover:text-ink">
        <Blocks class="size-5 flex-none text-ink-muted" aria-hidden="true" />
        <span class="min-w-0 flex-1">{{ t('kernel.modules.title') }}</span>
        <span v-if="modulesOn !== undefined" class="text-small text-ink-muted">{{ t('kernel.modules.on_count', { count: modulesOn }) }}</span>
        <ChevronRight class="size-4 flex-none text-ink-muted" aria-hidden="true" />
      </RouterLink>
      <RouterLink
        v-if="audited"
        to="/audit"
        class="flex min-h-12 items-center gap-3 border-t border-line px-4 text-ink no-underline hover:bg-surface-sunken hover:text-ink"
      >
        <ScrollText class="size-5 flex-none text-ink-muted" aria-hidden="true" />
        <span class="min-w-0 flex-1">{{ t('audit.page.title') }}</span>
        <ChevronRight class="size-4 flex-none text-ink-muted" aria-hidden="true" />
      </RouterLink>
      <RouterLink
        to="/settings"
        class="flex min-h-12 items-center gap-3 border-t border-line px-4 text-ink no-underline hover:bg-surface-sunken hover:text-ink"
      >
        <Settings class="size-5 flex-none text-ink-muted" aria-hidden="true" />
        <span class="min-w-0 flex-1">{{ t('kernel.settings.title') }}</span>
        <ChevronRight class="size-4 flex-none text-ink-muted" aria-hidden="true" />
      </RouterLink>
    </section>
    <section aria-labelledby="account" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
      <h2 id="account" class="m-0 text-heading font-semibold">{{ t('kernel.more.account') }}</h2>
      <p v-if="signedIn" class="m-0 text-ink-muted">{{ signedIn }}</p>
      <JecButton :busy="signOut.isPending.value" @click="leave"><LogOut class="size-4" aria-hidden="true" />{{ t('kernel.shell.sign_out') }}</JecButton>
    </section>
    <div class="flex items-center justify-between gap-2 px-1 text-small text-ink-muted">
      <LanguageSwitch class="flex-none" />
      <span v-if="manifest.data.value" class="min-w-0 truncate" :title="t('kernel.shell.version', { version: manifest.data.value.version })">
        {{ t('kernel.shell.version', { version: manifest.data.value.version }) }}
      </span>
    </div>
  </div>
</template>
