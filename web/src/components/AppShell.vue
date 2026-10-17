<script setup lang="ts">
import { Ellipsis, Globe, LayoutGrid, LogOut, Menu, Package, ShieldCheck, X } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'

import { useSession, useSignOut } from '../api/account'
import { useCertificates } from '../api/certs'
import { useManifest } from '../api/manifest'
import { useT, type MessageKey } from '../i18n'
import JecMark from './JecMark.vue'
import LanguageSwitch from './LanguageSwitch.vue'

// The frame of every signed-in page: from 1024px a sidebar that stays, from
// 640px one that opens from the menu button, and below that a tab bar at
// the bottom (docs/design/system/README.md, breakpoints).
const { t } = useT()
const route = useRoute()
const router = useRouter()
const session = useSession()
const signOut = useSignOut()
const manifest = useManifest()
const certificates = useCertificates()

interface Link {
  to: string
  label: MessageKey
  icon: typeof LayoutGrid
  count?: number
}

const links = computed<Link[]>(() => [
  { to: '/', label: 'kernel.overview.title', icon: LayoutGrid },
  { to: '/certificates', label: 'certs.list.title', icon: ShieldCheck, count: certificates.data.value?.length },
])
const tabs = computed<Link[]>(() => [...links.value, { to: '/more', label: 'kernel.shell.more', icon: Ellipsis }])
// Where providers are configured; on phones, the More tab lists them.
const config: Link[] = [
  { to: '/sources', label: 'kernel.providers.source.title', icon: Package },
  { to: '/dns', label: 'kernel.providers.solver.title', icon: Globe },
]

const current = (to: string) => (to === '/' ? route.path === '/' : route.path === to || route.path.startsWith(`${to}/`))

const drawer = ref(false)
watch(() => route.fullPath, () => (drawer.value = false))

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
  <div class="min-h-dvh lg:flex">
    <!-- Tablets: a bar with the menu button. -->
    <div class="sticky top-0 z-20 hidden h-14 items-center gap-2.5 border-b border-line bg-surface-raised px-2 sm:flex lg:hidden">
      <button
        type="button"
        :aria-label="t('kernel.shell.menu')"
        :aria-expanded="drawer ? 'true' : 'false'"
        class="grid size-11 cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink hover:bg-surface-sunken"
        @click="drawer = true"
      >
        <Menu class="size-5" aria-hidden="true" />
      </button>
      <JecMark :size="32" />
      <span class="font-semibold">{{ t('kernel.app.name') }}</span>
    </div>

    <div v-if="drawer" class="fixed inset-0 z-30 bg-[color-mix(in_srgb,var(--ink)_45%,transparent)] lg:hidden" @click="drawer = false" />

    <aside
      :class="[
        'z-40 w-[232px] flex-none flex-col gap-5 border-r border-line bg-surface-raised px-3 py-5',
        'lg:sticky lg:top-0 lg:flex lg:h-dvh',
        drawer ? 'fixed inset-y-0 left-0 flex overflow-y-auto' : 'hidden',
      ]"
    >
      <div class="flex items-center gap-2.5 px-2.5">
        <JecMark :size="32" />
        <span class="min-w-0 flex-1 text-heading font-semibold">{{ t('kernel.app.name') }}</span>
        <button
          v-if="drawer"
          type="button"
          :aria-label="t('kernel.shell.close_menu')"
          class="grid size-9 cursor-pointer place-items-center rounded-md border-0 bg-transparent text-ink hover:bg-surface-sunken lg:hidden"
          @click="drawer = false"
        >
          <X class="size-4" aria-hidden="true" />
        </button>
      </div>
      <nav :aria-label="t('kernel.shell.navigation')" class="flex flex-col gap-0.5">
        <RouterLink
          v-for="link in links"
          :key="link.to"
          :to="link.to"
          :aria-current="current(link.to) ? 'page' : undefined"
          :class="[
            'flex h-9 items-center gap-2.5 rounded-md px-2.5 font-medium no-underline',
            current(link.to) ? 'bg-brand-soft font-semibold text-ink hover:text-ink' : 'text-ink-muted hover:bg-surface-sunken hover:text-ink',
          ]"
        >
          <component :is="link.icon" :class="['size-[18px] flex-none', current(link.to) ? 'text-brand' : '']" aria-hidden="true" />
          <span class="min-w-0 flex-1 truncate" :title="t(link.label)">{{ t(link.label) }}</span>
          <data v-if="link.count !== undefined" :value="link.count" class="text-caption text-ink-muted tabular-nums">{{ link.count }}</data>
        </RouterLink>
      </nav>
      <nav :aria-label="t('kernel.shell.config')" class="flex flex-col gap-0.5">
        <span class="px-2.5 pb-1 text-caption font-semibold text-ink-muted">{{ t('kernel.shell.config') }}</span>
        <RouterLink
          v-for="link in config"
          :key="link.to"
          :to="link.to"
          :aria-current="current(link.to) ? 'page' : undefined"
          :class="[
            'flex h-9 items-center gap-2.5 rounded-md px-2.5 font-medium no-underline',
            current(link.to) ? 'bg-brand-soft font-semibold text-ink hover:text-ink' : 'text-ink-muted hover:bg-surface-sunken hover:text-ink',
          ]"
        >
          <component :is="link.icon" :class="['size-[18px] flex-none', current(link.to) ? 'text-brand' : '']" aria-hidden="true" />
          <span class="min-w-0 flex-1 truncate" :title="t(link.label)">{{ t(link.label) }}</span>
        </RouterLink>
      </nav>
      <div class="mt-auto flex flex-col gap-2 border-t border-line px-2.5 pt-3 text-caption text-ink-muted">
        <span v-if="signedIn" class="truncate" :title="signedIn">{{ signedIn }}</span>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <LanguageSwitch />
          <span v-if="manifest.data.value">{{ t('kernel.shell.version', { version: manifest.data.value.version }) }}</span>
        </div>
        <button
          type="button"
          class="flex h-9 cursor-pointer items-center gap-2 rounded-md border-0 bg-transparent px-0 text-small text-ink-muted hover:text-ink"
          :disabled="signOut.isPending.value"
          @click="leave"
        >
          <LogOut class="size-4" aria-hidden="true" />{{ t('kernel.shell.sign_out') }}
        </button>
      </div>
    </aside>

    <main class="mx-auto w-full max-w-[1184px] min-w-0 flex-1 pb-24 sm:px-8 sm:pt-6 sm:pb-8">
      <RouterView />
    </main>

    <!-- Phones: the tab bar. -->
    <nav
      :aria-label="t('kernel.shell.navigation')"
      class="fixed inset-x-0 bottom-0 z-20 flex border-t border-line bg-surface-raised px-2 pt-1 pb-[max(4px,env(safe-area-inset-bottom))] sm:hidden"
    >
      <RouterLink
        v-for="tab in tabs"
        :key="tab.to"
        :to="tab.to"
        :aria-current="current(tab.to) ? 'page' : undefined"
        :class="[
          'flex min-h-14 min-w-0 flex-1 flex-col items-center justify-center gap-0.5 text-caption no-underline',
          current(tab.to) ? 'font-semibold text-brand' : 'font-medium text-ink-muted hover:text-ink',
        ]"
      >
        <component :is="tab.icon" class="size-[22px]" aria-hidden="true" />
        <span class="max-w-full truncate" :title="t(tab.label)">{{ t(tab.label) }}</span>
      </RouterLink>
    </nav>
  </div>
</template>
