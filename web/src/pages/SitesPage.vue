<script setup lang="ts">
import { CircleCheck, CircleHelp, Clock, Globe, OctagonX, Plus, RefreshCw, ShieldAlert, Trash2, TriangleAlert, Unplug } from '@lucide/vue'
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { useCertificates } from '../api/certs'
import { useApiError } from '../api/errors'
import {
  useAddDomain,
  useAddSite,
  useCheckDomain,
  useCheckSite,
  useDeleteDomain,
  useDeleteSite,
  useDomains,
  useSites,
  type Domain,
  type Site,
} from '../api/watch'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import JecTextarea from '../components/JecTextarea.vue'
import PageHeader from '../components/PageHeader.vue'
import { ApiError, networkError } from '../api/client'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import { fromMore } from '../shell'

// What ends outside JEC's hands (module watch): the sites JEC watches but
// does not manage, with what each serves, whether it is trusted and when it
// ends, and the registrations of the domains. On phones lists, from 640px
// tables. Certificate transparency has a page of its own.
const { t } = useT()
const intl = useIntl()
const sites = useSites()
const loadError = useApiError(computed(() => sites.error.value))

type State = Site['state']
const looks: Record<State, { icon: typeof CircleCheck; label: MessageKey; badge: string; ink: string }> = {
  pending: { icon: Clock, label: 'watch.state.pending', badge: 'bg-surface-sunken text-ink-muted', ink: 'text-ink-muted' },
  ok: { icon: CircleCheck, label: 'watch.state.ok', badge: 'bg-ok-soft text-ok-ink', ink: 'text-ok-ink' },
  expiring: { icon: TriangleAlert, label: 'watch.state.expiring', badge: 'bg-warn-soft text-warn-ink', ink: 'text-warn-ink' },
  expired: { icon: OctagonX, label: 'watch.state.expired', badge: 'bg-danger-soft text-danger-ink', ink: 'text-danger-ink' },
  invalid: { icon: ShieldAlert, label: 'watch.state.invalid', badge: 'bg-danger-soft text-danger-ink', ink: 'text-danger-ink' },
  unreachable: { icon: Unplug, label: 'watch.state.unreachable', badge: 'bg-warn-soft text-warn-ink', ink: 'text-warn-ink' },
}
// A site that serves no intermediates looks as one to see to, not as fine.
const look = (s: Site) =>
  s.state === 'ok' && s.incomplete
    ? { icon: TriangleAlert, label: 'watch.state.incomplete' as MessageKey, badge: 'bg-warn-soft text-warn-ink', ink: 'text-warn-ink' }
    : looks[s.state]
const address = (s: Site) => (s.port === 443 ? s.host : s.host.includes(':') ? `[${s.host}]:${s.port}` : `${s.host}:${s.port}`)
// Why a site was not reached, in words, as the online check of a
// certificate says it; what the TLS library said, as it said it, else.
const reason = (s: Site) => {
  if (s.cause === 'dns') return t('monitor.state.no_dns')
  if (s.cause === 'connect') return t('monitor.state.no_connect', { port: s.port })
  if (s.cause === 'tls') return t('monitor.state.unreachable', { port: s.port })
  return s.error ?? ''
}
const day = 86_400_000
// How long is left, or how long ago it ended.
const until = (iso?: string) => {
  const days = Math.floor((Date.parse(iso ?? '') - Date.now()) / day)
  return days < 0 ? t('watch.page.days_ago', { days: -days }) : t('watch.page.days_left', { days })
}
const left = (s: Site) => until(s.notAfter)

// Adding: a host or a URL, and a name; or several, a line each, without
// names, those refused kept with why.
const adding = ref(false)
const host = ref('')
const name = ref('')
const add = useAddSite()
const addError = useApiError(computed(() => add.error.value))
const refused = ref<{ host: string; why: string }[]>([])
const hosts = computed(() => host.value.split('\n').map((h) => h.trim()).filter(Boolean))
function openAdd() {
  host.value = ''
  name.value = ''
  refused.value = []
  add.reset()
  adding.value = true
}
async function submitAdd() {
  if (hosts.value.length <= 1) {
    try {
      await add.mutateAsync({ host: hosts.value[0] ?? '', name: name.value || undefined })
    } catch {
      return // the dialog shows why
    }
    adding.value = false
    return
  }
  const failed: { host: string; why: string }[] = []
  for (const h of hosts.value) {
    try {
      await add.mutateAsync({ host: h })
    } catch (err) {
      const e = err instanceof ApiError ? err : networkError
      const field = e.fields.find((f) => f.location === 'body.host')
      failed.push({ host: h, why: t(field?.code ?? e.code, e.params) })
    }
  }
  add.reset()
  refused.value = failed
  host.value = failed.map((f) => f.host).join('\n')
  if (!failed.length) adding.value = false
}

const check = useCheckSite()
const checking = ref<number>()
async function checkNow(s: Site) {
  checking.value = s.id
  try {
    await check.mutateAsync(s.id)
  } catch {
    // the list shows the site as it was
  } finally {
    checking.value = undefined
  }
}

const deleting = ref<Site>()
const deleteOpen = computed({
  get: () => deleting.value !== undefined,
  set: (open) => {
    if (!open) deleting.value = undefined
  },
})
const remove = useDeleteSite()
const removeError = useApiError(computed(() => remove.error.value))
async function confirmDelete() {
  if (!deleting.value) return
  try {
    await remove.mutateAsync(deleting.value.id)
  } catch {
    return
  }
  deleting.value = undefined
}

// The domains: those of the certificates and sites, and those added by hand.
const domains = useDomains()
const domainsError = useApiError(computed(() => domains.error.value))
type DomainState = Domain['state']
const domainLooks: Record<DomainState, { icon: typeof CircleCheck; label: MessageKey; badge: string; ink: string }> = {
  pending: { icon: Clock, label: 'watch.domain.state.pending', badge: 'bg-surface-sunken text-ink-muted', ink: 'text-ink-muted' },
  ok: { icon: CircleCheck, label: 'watch.domain.state.ok', badge: 'bg-ok-soft text-ok-ink', ink: 'text-ok-ink' },
  expiring: { icon: TriangleAlert, label: 'watch.domain.state.expiring', badge: 'bg-warn-soft text-warn-ink', ink: 'text-warn-ink' },
  expired: { icon: OctagonX, label: 'watch.domain.state.expired', badge: 'bg-danger-soft text-danger-ink', ink: 'text-danger-ink' },
  unknown: { icon: CircleHelp, label: 'watch.domain.state.unknown', badge: 'bg-surface-sunken text-ink-muted', ink: 'text-ink-muted' },
  failed: { icon: Unplug, label: 'watch.domain.state.failed', badge: 'bg-warn-soft text-warn-ink', ink: 'text-warn-ink' },
}
// Why an end is not known, in words, with what the lookup said in the
// tooltip; a failed lookup's error as it said it.
const causes: Record<NonNullable<Domain['cause']>, MessageKey> = {
  no_lookup: 'watch.domain.cause.no_lookup',
  no_name: 'watch.domain.cause.no_name',
  no_end: 'watch.domain.cause.no_end',
  no_answer: 'watch.domain.cause.no_answer',
}
const domainNote = (d: Domain) =>
  d.cause
    ? { text: t(causes[d.cause]), title: t('watch.domain.error_detail', { error: d.error ?? '' }), translate: undefined }
    : { text: d.error ?? '', title: d.error ?? '', translate: 'no' as const }
// The certificates under a domain, by their primary domain.
const certificates = useCertificates()
const certName = computed(() => new Map((certificates.data.value ?? []).map((c) => [c.id, c.domains[0] ?? ''])))
const certsOf = (d: Domain) => d.certificates.filter((id) => certName.value.has(id)).map((id) => ({ id, name: certName.value.get(id) ?? '' }))

const addingDomain = ref(false)
const domainName = ref('')
const addDomain = useAddDomain()
const addDomainError = useApiError(computed(() => addDomain.error.value))
function openAddDomain() {
  domainName.value = ''
  addDomain.reset()
  addingDomain.value = true
}
async function submitAddDomain() {
  try {
    await addDomain.mutateAsync(domainName.value)
  } catch {
    return // the dialog shows why
  }
  addingDomain.value = false
}

const lookUp = useCheckDomain()
const lookingUp = ref<string>()
async function lookUpNow(d: Domain) {
  lookingUp.value = d.name
  try {
    await lookUp.mutateAsync(d.name)
  } catch {
    // the list shows the domain as it was
  } finally {
    lookingUp.value = undefined
  }
}

const droppingDomain = ref<Domain>()
const dropOpen = computed({
  get: () => droppingDomain.value !== undefined,
  set: (open) => {
    if (!open) droppingDomain.value = undefined
  },
})
const dropDomain = useDeleteDomain()
const dropError = useApiError(computed(() => dropDomain.error.value))
async function confirmDrop() {
  if (!droppingDomain.value) return
  try {
    await dropDomain.mutateAsync(droppingDomain.value.name)
  } catch {
    return
  }
  droppingDomain.value = undefined
}
</script>

<template>
  <PageHeader :title="t('watch.page.title')" :subtitle="t('watch.page.intro')" :phone-back="fromMore">
    <template #actions>
      <JecButton @click="openAddDomain"><Globe class="size-4" aria-hidden="true" />{{ t('watch.domain.add') }}</JecButton>
      <JecButton variant="primary" @click="openAdd"><Plus class="size-4" aria-hidden="true" />{{ t('watch.page.add') }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t('watch.page.intro') }}</p>
    <h2 class="m-0 text-section-title font-semibold">{{ t('watch.page.sites') }}</h2>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />

    <section v-if="sites.isSuccess.value && !sites.data.value?.length" class="flex flex-col items-start gap-3 rounded-lg border border-line bg-surface-raised p-4">
      <p class="m-0 text-ink-muted">{{ t('watch.page.empty') }}</p>
      <JecButton variant="primary" @click="openAdd"><Plus class="size-4" aria-hidden="true" />{{ t('watch.page.add') }}</JecButton>
    </section>

    <!-- From 640px: a table. -->
    <div v-if="sites.data.value?.length" class="hidden overflow-x-auto sm:block">
      <table class="w-full border-separate border-spacing-0 overflow-hidden rounded-lg border border-line bg-surface-raised text-small">
        <thead>
          <tr>
            <th
              v-for="col in ['watch.column.site', 'watch.column.state', 'watch.column.ends', 'watch.column.issuer', 'watch.column.checked'] as const"
              :key="col"
              scope="col"
              class="border-b border-line bg-surface-sunken px-3 py-2 text-left font-semibold whitespace-nowrap text-ink-muted"
            >
              {{ t(col) }}
            </th>
            <th scope="col" class="border-b border-line bg-surface-sunken px-3 py-2"><span class="sr-only">{{ t('watch.column.actions') }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in sites.data.value" :key="s.id" class="align-top hover:bg-surface-sunken">
            <td class="border-b border-line px-3 py-2 [overflow-wrap:break-word]">
              <span v-if="s.name" class="block font-semibold" translate="no">{{ s.name }}</span>
              <!-- Broken after a dot, where a name must be. -->
              <span :class="['block', s.name ? 'text-caption text-ink-muted' : 'font-semibold']" translate="no"
                ><template v-for="(label, i) in address(s).split(/(?<=\.)/)" :key="i"><wbr v-if="i" />{{ label }}</template></span
              >
            </td>
            <td class="border-b border-line px-3 py-2">
              <span :class="['inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-semibold whitespace-nowrap', look(s).badge]">
                <component :is="look(s).icon" class="size-3.5" aria-hidden="true" />{{ t(look(s).label) }}
              </span>
              <span v-if="s.incomplete" class="mt-1 block max-w-64 text-caption font-semibold text-warn-ink">{{ t('watch.site.incomplete') }}</span>
              <span
                v-if="s.error"
                class="mt-1 block max-w-64 truncate text-caption text-ink-muted"
                :title="s.cause ? t('watch.site.error_detail', { error: s.error }) : s.error"
                :translate="s.cause ? undefined : 'no'"
                >{{ reason(s) }}</span
              >
            </td>
            <td class="border-b border-line px-3 py-2 whitespace-nowrap">
              <template v-if="s.notAfter">
                <time :datetime="s.notAfter" class="block">{{ intl.date(s.notAfter) }}</time>
                <span :class="['block text-caption', looks[s.state].ink]">{{ left(s) }}</span>
              </template>
              <span v-else class="text-ink-muted">{{ t('watch.page.unknown') }}</span>
            </td>
            <td class="border-b border-line px-3 py-2 [overflow-wrap:anywhere] text-ink-muted" translate="no">{{ s.issuer }}</td>
            <td class="border-b border-line px-3 py-2 whitespace-nowrap text-ink-muted">
              <time v-if="s.checkedAt" :datetime="s.checkedAt">{{ intl.dateTime(s.checkedAt) }}</time>
            </td>
            <td class="w-24 border-b border-line px-1 py-1 whitespace-nowrap">
              <JecButton
                size="sm"
                variant="ghost"
                :busy="checking === s.id"
                :aria-label="t('watch.page.check', { site: address(s) })"
                :title="t('watch.page.check', { site: address(s) })"
                @click="checkNow(s)"
              >
                <RefreshCw class="size-4" aria-hidden="true" />
              </JecButton>
              <JecButton
                size="sm"
                variant="ghost"
                :aria-label="t('watch.page.delete', { site: address(s) })"
                :title="t('watch.page.delete', { site: address(s) })"
                @click="deleting = s"
              >
                <Trash2 class="size-4" aria-hidden="true" />
              </JecButton>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Phones: a list. -->
    <ul v-if="sites.data.value?.length" class="m-0 list-none overflow-hidden rounded-lg border border-line bg-surface-raised p-0 sm:hidden">
      <li v-for="s in sites.data.value" :key="s.id" class="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 border-b border-line px-4 py-2.5 last:border-b-0">
        <component :is="look(s).icon" :class="['mt-0.5 size-5', look(s).ink]" aria-hidden="true" />
        <span class="grid min-w-0 gap-0.5">
          <span class="truncate font-semibold" translate="no" :title="s.name || address(s)">{{ s.name || address(s) }}</span>
          <span v-if="s.name" class="truncate text-caption text-ink-muted" translate="no" :title="address(s)">{{ address(s) }}</span>
          <span class="flex flex-wrap gap-x-2 text-small">
            <span :class="['font-semibold', look(s).ink]">{{ t(look(s).label) }}</span>
            <template v-if="s.notAfter">
              <time :datetime="s.notAfter" class="text-ink-muted">{{ intl.date(s.notAfter) }}</time>
              <span class="text-ink-muted">{{ left(s) }}</span>
            </template>
          </span>
          <span v-if="s.incomplete" class="text-caption font-semibold text-warn-ink">{{ t('watch.site.incomplete') }}</span>
          <span
            v-if="s.error"
            class="truncate text-caption text-ink-muted"
            :title="s.cause ? t('watch.site.error_detail', { error: s.error }) : s.error"
            :translate="s.cause ? undefined : 'no'"
            >{{ reason(s) }}</span
          >
        </span>
        <span class="flex flex-none">
          <JecButton size="sm" variant="ghost" :busy="checking === s.id" :aria-label="t('watch.page.check', { site: address(s) })" @click="checkNow(s)">
            <RefreshCw class="size-4" aria-hidden="true" />
          </JecButton>
          <JecButton size="sm" variant="ghost" :aria-label="t('watch.page.delete', { site: address(s) })" @click="deleting = s">
            <Trash2 class="size-4" aria-hidden="true" />
          </JecButton>
        </span>
      </li>
    </ul>

    <JecButton v-if="sites.data.value?.length" class="sm:hidden" @click="openAdd"><Plus class="size-4" aria-hidden="true" />{{ t('watch.page.add') }}</JecButton>
    <p class="m-0 text-small text-ink-muted">{{ t('watch.page.foot') }}</p>

    <h2 class="m-0 mt-2 text-section-title font-semibold">{{ t('watch.domain.title') }}</h2>
    <JecAlert v-if="domainsError.error.value" :code="domainsError.error.value.code" :params="domainsError.error.value.params" />
    <section v-if="domains.isSuccess.value && !domains.data.value?.length" class="flex flex-col items-start gap-3 rounded-lg border border-line bg-surface-raised p-4">
      <p class="m-0 text-ink-muted">{{ t('watch.domain.empty') }}</p>
      <JecButton @click="openAddDomain"><Globe class="size-4" aria-hidden="true" />{{ t('watch.domain.add') }}</JecButton>
    </section>

    <!-- From 640px: a table. -->
    <div v-if="domains.data.value?.length" class="hidden overflow-x-auto sm:block">
      <table class="w-full border-separate border-spacing-0 overflow-hidden rounded-lg border border-line bg-surface-raised text-small">
        <thead>
          <tr>
            <th
              v-for="col in ['watch.domain.column.name', 'watch.column.state', 'watch.column.ends', 'watch.domain.column.registrar', 'watch.domain.column.uses', 'watch.domain.column.checked'] as const"
              :key="col"
              scope="col"
              class="border-b border-line bg-surface-sunken px-3 py-2 text-left font-semibold whitespace-nowrap text-ink-muted"
            >
              {{ t(col) }}
            </th>
            <th scope="col" class="border-b border-line bg-surface-sunken px-3 py-2"><span class="sr-only">{{ t('watch.column.actions') }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in domains.data.value" :key="d.name" class="align-top hover:bg-surface-sunken">
            <td class="border-b border-line px-3 py-2 font-semibold [overflow-wrap:anywhere]" translate="no">{{ d.name }}</td>
            <td class="border-b border-line px-3 py-2">
              <span :class="['inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-semibold whitespace-nowrap', domainLooks[d.state].badge]">
                <component :is="domainLooks[d.state].icon" class="size-3.5" aria-hidden="true" />{{ t(domainLooks[d.state].label) }}
              </span>
              <span v-if="d.error" class="mt-1 block max-w-64 truncate text-caption text-ink-muted" :title="domainNote(d).title" :translate="domainNote(d).translate">{{
                domainNote(d).text
              }}</span>
            </td>
            <td class="border-b border-line px-3 py-2 whitespace-nowrap">
              <template v-if="d.expiresAt">
                <time :datetime="d.expiresAt" class="block">{{ intl.date(d.expiresAt) }}</time>
                <span :class="['block text-caption', domainLooks[d.state].ink]">{{ until(d.expiresAt) }}</span>
              </template>
              <span v-else class="text-ink-muted">{{ t('watch.page.unknown') }}</span>
            </td>
            <td class="border-b border-line px-3 py-2 [overflow-wrap:anywhere] text-ink-muted" translate="no">{{ d.registrar }}</td>
            <td class="border-b border-line px-3 py-2">
              <span class="flex flex-col gap-0.5">
                <RouterLink v-for="c in certsOf(d)" :key="c.id" :to="`/certificates/${c.id}`" class="underline [overflow-wrap:anywhere]" translate="no">{{ c.name }}</RouterLink>
                <span v-if="d.sites.length" class="text-ink-muted">{{ t('watch.domain.sites', { count: d.sites.length }) }}</span>
                <span v-if="d.manual" class="text-ink-muted">{{ t('watch.domain.manual') }}</span>
              </span>
            </td>
            <td class="border-b border-line px-3 py-2 whitespace-nowrap text-ink-muted">
              <time v-if="d.checkedAt" :datetime="d.checkedAt">{{ intl.dateTime(d.checkedAt) }}</time>
            </td>
            <td class="w-24 border-b border-line px-1 py-1 whitespace-nowrap">
              <JecButton
                size="sm"
                variant="ghost"
                :busy="lookingUp === d.name"
                :aria-label="t('watch.domain.check', { domain: d.name })"
                :title="t('watch.domain.check', { domain: d.name })"
                @click="lookUpNow(d)"
              >
                <RefreshCw class="size-4" aria-hidden="true" />
              </JecButton>
              <JecButton
                v-if="d.manual"
                size="sm"
                variant="ghost"
                :aria-label="t('watch.domain.delete', { domain: d.name })"
                :title="t('watch.domain.delete', { domain: d.name })"
                @click="droppingDomain = d"
              >
                <Trash2 class="size-4" aria-hidden="true" />
              </JecButton>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Phones: a list. -->
    <ul v-if="domains.data.value?.length" class="m-0 list-none overflow-hidden rounded-lg border border-line bg-surface-raised p-0 sm:hidden">
      <li v-for="d in domains.data.value" :key="d.name" class="grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 border-b border-line px-4 py-2.5 last:border-b-0">
        <component :is="domainLooks[d.state].icon" :class="['mt-0.5 size-5', domainLooks[d.state].ink]" aria-hidden="true" />
        <span class="grid min-w-0 gap-0.5">
          <span class="truncate font-semibold" translate="no" :title="d.name">{{ d.name }}</span>
          <span class="flex flex-wrap gap-x-2 text-small">
            <span :class="['font-semibold', domainLooks[d.state].ink]">{{ t(domainLooks[d.state].label) }}</span>
            <template v-if="d.expiresAt">
              <time :datetime="d.expiresAt" class="text-ink-muted">{{ intl.date(d.expiresAt) }}</time>
              <span class="text-ink-muted">{{ until(d.expiresAt) }}</span>
            </template>
          </span>
          <span v-if="d.registrar" class="truncate text-caption text-ink-muted" translate="no" :title="d.registrar">{{ d.registrar }}</span>
          <span v-if="d.error" class="truncate text-caption text-ink-muted" :title="domainNote(d).title" :translate="domainNote(d).translate">{{ domainNote(d).text }}</span>
        </span>
        <span class="flex flex-none">
          <JecButton size="sm" variant="ghost" :busy="lookingUp === d.name" :aria-label="t('watch.domain.check', { domain: d.name })" @click="lookUpNow(d)">
            <RefreshCw class="size-4" aria-hidden="true" />
          </JecButton>
          <JecButton v-if="d.manual" size="sm" variant="ghost" :aria-label="t('watch.domain.delete', { domain: d.name })" @click="droppingDomain = d">
            <Trash2 class="size-4" aria-hidden="true" />
          </JecButton>
        </span>
      </li>
    </ul>

    <JecButton v-if="domains.data.value?.length" class="sm:hidden" @click="openAddDomain"><Globe class="size-4" aria-hidden="true" />{{ t('watch.domain.add') }}</JecButton>
    <p class="m-0 text-small text-ink-muted">{{ t('watch.domain.foot') }}</p>

  </div>

  <JecDialog v-model:open="adding" :title="t('watch.page.add')" small>
    <form id="site-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitAdd">
      <JecAlert v-if="addError.alert.value" :code="addError.alert.value.code" :params="addError.alert.value.params" />
      <JecField id="site-host" label="watch.field.host" hint="watch.field.host_help" :error="addError.fields.value.host">
        <JecTextarea id="site-host" v-model="host" :rows="2" :invalid="!!addError.fields.value.host" required />
      </JecField>
      <div v-if="refused.length" role="alert">
        <ul class="m-0 list-none p-0 text-small text-danger-ink">
          <li v-for="r in refused" :key="r.host" class="[overflow-wrap:anywhere]"><span translate="no">{{ r.host }}</span>{{ t('watch.page.refused') }}{{ r.why }}</li>
        </ul>
      </div>
      <JecField v-if="hosts.length <= 1" id="site-name" label="watch.field.name" hint="watch.field.name_help">
        <JecInput id="site-name" v-model="name" autocomplete="off" />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="adding = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="primary" type="submit" form="site-form" :busy="add.isPending.value">
        <Plus class="size-4" aria-hidden="true" />{{ t('watch.page.add') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="deleteOpen" :title="t('watch.page.delete', { site: deleting ? address(deleting) : '' })" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('watch.page.delete_body') }}</p>
      <JecAlert v-if="removeError.error.value" :code="removeError.error.value.code" :params="removeError.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="deleteOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="danger" :busy="remove.isPending.value" @click="confirmDelete">
        <Trash2 class="size-4" aria-hidden="true" />{{ t('kernel.action.delete') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="addingDomain" :title="t('watch.domain.add')" small>
    <form id="domain-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitAddDomain">
      <JecAlert v-if="addDomainError.alert.value" :code="addDomainError.alert.value.code" :params="addDomainError.alert.value.params" />
      <JecField id="domain-name" label="watch.domain.field.name" hint="watch.domain.field.name_help" :error="addDomainError.fields.value.name">
        <JecInput id="domain-name" v-model="domainName" :invalid="!!addDomainError.fields.value.name" autocomplete="off" required />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="addingDomain = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="primary" type="submit" form="domain-form" :busy="addDomain.isPending.value">
        <Globe class="size-4" aria-hidden="true" />{{ t('watch.domain.add') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="dropOpen" :title="t('watch.domain.delete', { domain: droppingDomain?.name ?? '' })" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('watch.domain.delete_body') }}</p>
      <JecAlert v-if="dropError.error.value" :code="dropError.error.value.code" :params="dropError.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="dropOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="danger" :busy="dropDomain.isPending.value" @click="confirmDrop">
        <Trash2 class="size-4" aria-hidden="true" />{{ t('kernel.action.delete') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
