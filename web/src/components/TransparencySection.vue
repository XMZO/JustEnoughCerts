<script setup lang="ts">
import { CircleCheck, Clock, EyeOff, ExternalLink, KeyRound, Plus, Radar, RefreshCw, ScanSearch, ShieldCheck, ShieldQuestion, Undo2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { useApiError } from '../api/errors'
import {
  useAddSite,
  useJudgeSpotted,
  useSaveTransparency,
  useSearchTransparency,
  useTransparency,
  useUntrust,
  type Spotted,
  type TransparencyTrust,
} from '../api/watch'
import { useFitRows } from '../fit'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'
import JecPager from './JecPager.vue'
import RowMenu, { type MenuItem } from './RowMenu.vue'

// Certificate transparency (module watch): the certificates logged for the
// domains JEC holds certificates under, through Cert Spotter; those JEC
// does not hold are listed, to trust (as a CDN's) or ignore. Off until
// switched on, as it sends the domains to SSLMate. A CDN or another tool
// renews the same certificate again and again: those of the same names by
// the same issuer, the kind a trust covers, make one row, and the rows
// come a page at a time, as many as fit the window.
const { t } = useT()
const intl = useIntl()
const ct = useTransparency()
const loadError = useApiError(computed(() => ct.error.value))
const save = useSaveTransparency()
const saveError = useApiError(computed(() => save.error.value))
const search = useSearchTransparency()
const judge = useJudgeSpotted()
const judgeError = useApiError(computed(() => judge.error.value))
const untrust = useUntrust()
// Watched as a site: the first of its names that is not a wildcard.
const watchSite = useAddSite()
const watchError = useApiError(computed(() => watchSite.error.value))
const siteOf = (s: Spotted) => s.names.find((n) => !n.startsWith('*.'))
async function watchIt(k: Kind) {
  const host = siteOf(k.latest)
  if (!host) return
  busy.value = k.key + 'watch'
  try {
    await watchSite.mutateAsync({ host })
  } catch {
    // the alert shows why
  } finally {
    busy.value = undefined
  }
}

async function toggle() {
  const on = ct.data.value?.enabled ?? false
  try {
    await save.mutateAsync({ enabled: !on })
  } catch {
    // the alert shows why
  }
}

const looks: Record<Spotted['state'], { icon: typeof CircleCheck; label: MessageKey; badge: string; ink: string }> = {
  unknown: { icon: ShieldQuestion, label: 'watch.ct.state.unknown', badge: 'bg-warn-soft text-warn-ink', ink: 'text-warn-ink' },
  new: { icon: Clock, label: 'watch.ct.state.new', badge: 'bg-surface-sunken text-ink-muted', ink: 'text-ink-muted' },
  baseline: { icon: Clock, label: 'watch.ct.state.baseline', badge: 'bg-surface-sunken text-ink-muted', ink: 'text-ink-muted' },
}
const names = (s: { names: string[] }) => s.names.join(', ')
const failing = computed(() => (ct.data.value?.domains ?? []).filter((d) => d.error))
const lastSearch = computed(() => {
  const times = (ct.data.value?.domains ?? []).map((d) => d.checkedAt).filter((v): v is string => !!v)
  return times.sort().at(-1)
})

// The certificates of the same names by the same issuer, the newest
// first, as one row: told of if any is, and judged together.
interface Kind {
  key: string
  latest: Spotted
  all: Spotted[]
  state: Spotted['state']
}
const worse: Spotted['state'][] = ['baseline', 'new', 'unknown']
const kinds = computed(() => {
  const out = new Map<string, Kind>()
  for (const s of ct.data.value?.certificates ?? []) {
    const key = `${[...s.names].sort().join(',')}|${s.issuer}`
    const k = out.get(key)
    if (!k) out.set(key, { key, latest: s, all: [s], state: s.state })
    else {
      k.all.push(s)
      if (worse.indexOf(s.state) > worse.indexOf(k.state)) k.state = s.state
    }
  }
  return [...out.values()]
})
// Under the rows: the pager, the trusted kinds and the note, then the
// page's margin (on phones, the tab bar's).
const list = ref<HTMLElement>()
const after = ref<HTMLElement>()
const size = useFitRows(list, {
  row: () => (window.innerWidth >= 640 ? 58 : 110),
  below: () => (after.value?.offsetHeight ?? 0) + (window.innerWidth >= 640 ? 48 : 108),
  again: () => ct.data.value,
})
const page = ref(1)
const pages = computed(() => Math.max(1, Math.ceil(kinds.value.length / Math.max(1, size.value))))
watch(pages, (n) => {
  if (page.value > n) page.value = n
})
const shown = computed(() => kinds.value.slice((page.value - 1) * size.value, page.value * size.value))

const busy = ref<string>()
async function act(k: Kind, how: 'trust' | 'ignore') {
  busy.value = k.key + how
  try {
    // A trust covers the kind; each certificate is ignored on its own.
    for (const s of how === 'trust' ? [k.latest] : k.all) await judge.mutateAsync({ id: s.id, how })
  } catch {
    // the alert shows why
  } finally {
    busy.value = undefined
  }
}
const menu = (k: Kind): MenuItem[] => [
  ...(siteOf(k.latest) ? [{ key: 'watch', label: t('watch.ct.watch'), hint: t('watch.ct.watch_help'), icon: Radar, run: () => void watchIt(k) }] : []),
  { key: 'manage', label: t('watch.ct.manage'), hint: t('watch.ct.manage_help'), icon: Plus, to: { path: '/certificates/new', query: { domains: k.latest.names.join(',') } } },
  { key: 'view', label: t('watch.ct.view'), icon: ExternalLink, run: () => void window.open(`https://crt.sh/?sha256=${k.latest.sha256}`, '_blank', 'noopener') },
]
async function drop(tr: TransparencyTrust) {
  try {
    await untrust.mutateAsync({ names: tr.names, issuer: tr.issuer })
  } catch {
    // the list stays as it was
  }
}

// The API key: optional, for more searches an hour.
const keyOpen = ref(false)
const key = ref('')
function openKey() {
  key.value = ''
  save.reset()
  keyOpen.value = true
}
async function saveKey(clear = false) {
  try {
    await save.mutateAsync({ enabled: ct.data.value?.enabled ?? false, apiKey: clear ? '' : key.value })
  } catch {
    return
  }
  keyOpen.value = false
}
</script>

<template>
  <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />
  <section v-if="ct.data.value" class="flex flex-col gap-3 rounded-lg border border-line bg-surface-raised p-4">
    <div class="flex flex-wrap items-start gap-3">
      <div class="min-w-0 flex-[1_1_18rem]">
        <p class="m-0">{{ t('watch.ct.intro') }}</p>
        <p v-if="ct.data.value.enabled" class="m-0 mt-1 text-small text-ink-muted">
          {{ t('watch.ct.status', { domains: ct.data.value.domains.length, own: ct.data.value.own }) }}
          <template v-if="lastSearch">
            {{ t('watch.ct.last') }} <time :datetime="lastSearch">{{ intl.dateTime(lastSearch) }}</time>
          </template>
        </p>
      </div>
      <span v-if="ct.data.value.enabled" class="inline-flex flex-none items-center gap-1 text-small font-semibold text-ok-ink">
        <CircleCheck class="size-4" aria-hidden="true" />{{ t('watch.ct.on') }}
      </span>
      <span v-else class="inline-flex flex-none items-center gap-1 text-small text-ink-muted">{{ t('watch.ct.off') }}</span>
    </div>
    <JecAlert v-if="saveError.error.value && !keyOpen" :code="saveError.error.value.code" :params="saveError.error.value.params" />
    <p v-for="d in failing" :key="d.name" class="m-0 text-small text-warn-ink [overflow-wrap:anywhere]">
      <span translate="no">{{ d.name }}</span>{{ t('watch.ct.failed') }} <span translate="no">{{ d.error }}</span>
    </p>
    <div class="flex flex-wrap gap-2">
      <JecButton :variant="ct.data.value.enabled ? 'default' : 'primary'" :busy="save.isPending.value && !keyOpen" @click="toggle">
        <ScanSearch class="size-4" aria-hidden="true" />{{ t(ct.data.value.enabled ? 'watch.ct.switch_off' : 'watch.ct.switch_on') }}
      </JecButton>
      <JecButton v-if="ct.data.value.enabled" :busy="search.isPending.value" @click="search.mutate()">
        <RefreshCw class="size-4" aria-hidden="true" />{{ t('watch.ct.search') }}
      </JecButton>
      <JecButton @click="openKey"><KeyRound class="size-4" aria-hidden="true" />{{ t('watch.ct.key') }}</JecButton>
    </div>
  </section>

  <template v-if="ct.data.value?.enabled">
    <JecAlert v-if="judgeError.error.value" :code="judgeError.error.value.code" :params="judgeError.error.value.params" />
    <JecAlert v-if="watchError.alert.value" :code="watchError.alert.value.code" :params="watchError.alert.value.params" />
    <p v-if="!ct.data.value.certificates.length" class="m-0 text-ink-muted">{{ t('watch.ct.none') }}</p>
    <ul v-else ref="list" class="m-0 list-none overflow-hidden rounded-lg border border-line bg-surface-raised p-0">
      <li
        v-for="k in shown"
        :key="k.key"
        data-row
        class="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 gap-y-1.5 border-b border-line px-4 py-2 last:border-b-0 sm:grid-cols-[auto_minmax(0,1fr)_auto]"
      >
        <component :is="looks[k.state].icon" :class="['size-5 self-start sm:self-center', looks[k.state].ink]" aria-hidden="true" />
        <span class="grid min-w-0 gap-0.5">
          <span class="truncate font-semibold" translate="no" :title="names(k.latest)">{{ names(k.latest) }}</span>
          <span class="flex min-w-0 flex-wrap items-center gap-x-2 text-small">
            <span :class="['inline-flex items-center rounded-sm px-1.5 font-semibold', looks[k.state].badge]">{{ t(looks[k.state].label) }}</span>
            <span class="max-w-60 truncate text-ink-muted" translate="no" :title="k.latest.issuerDn">{{ k.latest.issuer }}</span>
            <span v-if="k.all.length > 1" class="font-semibold text-ink-muted">{{ t('watch.ct.trusted_count', { count: k.all.length }) }}</span>
            <span class="text-ink-muted">
              <template v-if="k.all.length > 1">{{ t('watch.ct.latest') }}</template>
              <time :datetime="k.latest.notBefore">{{ intl.date(k.latest.notBefore) }}</time>{{ t('watch.ct.to') }}<time :datetime="k.latest.notAfter">{{
                intl.date(k.latest.notAfter)
              }}</time>
            </span>
          </span>
        </span>
        <span class="col-start-2 flex items-center gap-1 sm:col-start-3">
          <JecButton size="sm" :busy="busy === k.key + 'trust'" :title="t('watch.ct.trust_help')" @click="act(k, 'trust')">
            <ShieldCheck class="size-4" aria-hidden="true" />{{ t('watch.ct.trust') }}
          </JecButton>
          <JecButton size="sm" variant="ghost" :busy="busy === k.key + 'ignore'" @click="act(k, 'ignore')">
            <EyeOff class="size-4" aria-hidden="true" />{{ t('watch.ct.ignore') }}
          </JecButton>
          <JecButton v-if="busy === k.key + 'watch'" size="sm" variant="ghost" busy>{{ t('watch.ct.watch') }}</JecButton>
          <RowMenu :label="t('watch.ct.more', { names: names(k.latest) })" :items="menu(k)" />
        </span>
      </li>
    </ul>
    <div ref="after" class="flex flex-col gap-3">
      <JecPager v-model="page" :pages="pages" />
      <div v-if="ct.data.value.trusted.length" class="flex flex-col gap-1">
        <h2 class="m-0 text-small font-semibold text-ink-muted">{{ t('watch.ct.trusted') }}</h2>
        <ul class="m-0 list-none p-0">
          <li v-for="tr in ct.data.value.trusted" :key="names(tr) + tr.issuer" class="flex flex-wrap items-center gap-x-2 text-small">
            <span class="[overflow-wrap:anywhere]" translate="no">{{ names(tr) }}</span>
            <span class="text-ink-muted" translate="no">{{ tr.issuer }}</span>
            <span class="text-ink-muted">{{ t('watch.ct.trusted_count', { count: tr.count ?? 0 }) }}</span>
            <JecButton size="sm" variant="ghost" :busy="untrust.isPending.value" @click="drop(tr)">
              <Undo2 class="size-4" aria-hidden="true" />{{ t('watch.ct.untrust') }}
            </JecButton>
          </li>
        </ul>
      </div>
      <p class="m-0 text-small text-ink-muted">
        {{ t('watch.ct.foot') }}
        <template v-if="ct.data.value.ignored">{{ t('watch.ct.ignored', { count: ct.data.value.ignored }) }}</template>
      </p>
    </div>
  </template>

  <JecDialog v-model:open="keyOpen" :title="t('watch.ct.key')" small>
    <form id="ct-key-form" class="flex flex-col gap-3" novalidate @submit.prevent="saveKey()">
      <p class="m-0">{{ t('watch.ct.key_intro') }}</p>
      <JecAlert v-if="saveError.error.value" :code="saveError.error.value.code" :params="saveError.error.value.params" />
      <JecField id="ct-key" label="watch.ct.key_field" :hint="ct.data.value?.hasKey ? 'watch.ct.key_kept' : undefined">
        <JecInput id="ct-key" v-model="key" type="password" autocomplete="off" />
      </JecField>
    </form>
    <template #footer>
      <JecButton v-if="ct.data.value?.hasKey" variant="ghost" class="mr-auto" @click="saveKey(true)">{{ t('watch.ct.key_clear') }}</JecButton>
      <JecButton @click="keyOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="ct-key-form" variant="primary" :busy="save.isPending.value" :disabled="!key">{{ t('kernel.action.save') }}</JecButton>
    </template>
  </JecDialog>
</template>
