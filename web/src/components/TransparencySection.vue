<script setup lang="ts">
import { CircleCheck, Clock, EyeOff, ExternalLink, KeyRound, RefreshCw, ScanSearch, ShieldCheck, ShieldQuestion, Undo2 } from '@lucide/vue'
import { computed, ref } from 'vue'

import { useApiError } from '../api/errors'
import {
  useJudgeSpotted,
  useSaveTransparency,
  useSearchTransparency,
  useTransparency,
  useUntrust,
  type Spotted,
  type TransparencyTrust,
} from '../api/watch'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'

// Certificate transparency (module watch): the certificates logged for the
// domains JEC holds certificates under, through Cert Spotter; those JEC
// does not hold are listed, to trust (as a CDN's) or ignore. Off until
// switched on, as it sends the domains to SSLMate.
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

const busy = ref<string>()
async function act(s: Spotted, how: 'trust' | 'ignore') {
  busy.value = s.id + how
  try {
    await judge.mutateAsync({ id: s.id, how })
  } catch {
    // the alert shows why
  } finally {
    busy.value = undefined
  }
}
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
  <h2 class="m-0 mt-2 text-section-title font-semibold">{{ t('watch.ct.title') }}</h2>
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
    <p v-if="!ct.data.value.certificates.length" class="m-0 text-ink-muted">{{ t('watch.ct.none') }}</p>
    <ul v-else class="m-0 list-none overflow-hidden rounded-lg border border-line bg-surface-raised p-0">
      <li
        v-for="s in ct.data.value.certificates"
        :key="s.id"
        class="grid grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-2 border-b border-line px-4 py-2.5 last:border-b-0 sm:grid-cols-[auto_minmax(0,1fr)_auto]"
      >
        <component :is="looks[s.state].icon" :class="['mt-0.5 size-5', looks[s.state].ink]" aria-hidden="true" />
        <span class="grid min-w-0 gap-0.5">
          <span class="font-semibold [overflow-wrap:anywhere]" translate="no">{{ names(s) }}</span>
          <span class="flex flex-wrap items-center gap-x-2 text-small">
            <span :class="['inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-semibold', looks[s.state].badge]">{{ t(looks[s.state].label) }}</span>
            <span class="text-ink-muted [overflow-wrap:anywhere]" translate="no" :title="s.issuerDn">{{ s.issuer }}</span>
            <span class="text-ink-muted">
              <time :datetime="s.notBefore">{{ intl.date(s.notBefore) }}</time>{{ t('watch.ct.to') }}<time :datetime="s.notAfter">{{ intl.date(s.notAfter) }}</time>
            </span>
          </span>
        </span>
        <span class="col-start-2 flex flex-wrap gap-1 sm:col-start-3">
          <JecButton size="sm" :busy="busy === s.id + 'trust'" :title="t('watch.ct.trust_help')" @click="act(s, 'trust')">
            <ShieldCheck class="size-4" aria-hidden="true" />{{ t('watch.ct.trust') }}
          </JecButton>
          <JecButton size="sm" variant="ghost" :busy="busy === s.id + 'ignore'" @click="act(s, 'ignore')">
            <EyeOff class="size-4" aria-hidden="true" />{{ t('watch.ct.ignore') }}
          </JecButton>
          <a
            :href="`https://crt.sh/?sha256=${s.sha256}`"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex min-h-[30px] items-center gap-1 px-2 text-small underline max-sm:min-h-11"
          >
            <ExternalLink class="size-4" aria-hidden="true" />{{ t('watch.ct.view') }}
          </a>
        </span>
      </li>
    </ul>
    <div v-if="ct.data.value.trusted.length" class="flex flex-col gap-1">
      <h3 class="m-0 text-small font-semibold text-ink-muted">{{ t('watch.ct.trusted') }}</h3>
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
