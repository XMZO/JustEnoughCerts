<script setup lang="ts">
import { ChevronRight, CircleCheck, CircleSlash, CircleX, LoaderCircle, OctagonX, Plus, Search, TriangleAlert } from '@lucide/vue'
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { ApiError, networkError } from '../api/client'
import { useCertificates, useRuns, type Certificate, type CertificateState } from '../api/certs'
import { useApiError } from '../api/errors'
import { useManifest } from '../api/manifest'
import { useProviders } from '../api/providers'
import { needsLook, useSites } from '../api/watch'
import { useCertActions } from '../certs/actions'
import { failingDeployment, useDeployNote } from '../certs/deployment'
import { runAt, runLink, runResult, useRunText } from '../certs/runs'
import { certLife, lifeText, needsAttention, type Life } from '../certs/life'
import CertActionDialog, { type CertAsk } from '../components/CertActionDialog.vue'
import CertSlot from '../components/CertSlot.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import LifeBar from '../components/LifeBar.vue'
import PageHeader from '../components/PageHeader.vue'
import SearchBox from '../components/SearchBox.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'
import { dayKey } from '../zone'

// The overview (docs/design/boards/Main and OverviewMobile): every
// certificate as a slot, the selected one's summary, what needs attention
// and what expires in the next 90 days.
const { t } = useT()
const intl = useIntl()
const certificates = useCertificates()
const query = ref('')

const error = computed(() => {
  const err = certificates.error.value
  if (!err) return null
  return err instanceof ApiError ? err : networkError
})

const all = computed(() => certificates.data.value ?? [])
const sources = useProviders('source')
const targets = useProviders('target')
const sourceName = (c: Certificate) => sources.data.value?.find((p) => p.id === c.sourceProvider)?.name ?? ''
const targetNames = (c: Certificate) => c.targets.map((id) => targets.data.value?.find((p) => p.id === id)?.name ?? '').filter(Boolean)
const deployNote = useDeployNote()
const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q ? all.value.filter((c) => c.domains.some((d) => d.includes(q))) : all.value
})
const attention = computed(() => all.value.filter(needsAttention))

const picked = ref<number | null>(null)
const selected = computed(() => shown.value.find((c) => c.id === picked.value) ?? shown.value[0] ?? null)
const selectedLife = computed(() => (selected.value ? certLife(selected.value) : null))
const selectedExtra = computed(() => {
  const n = (selected.value?.domains.length ?? 0) - 1
  return n > 0 ? `+${n}` : ''
})

// The certificates in their renewal window that renew by themselves need no
// one: they show apart, with when they renew.
const renewing = computed(() => attention.value.filter((c) => c.source === 'provider' && c.state === 'expiring'))
const firstRenewal = computed(() => {
  const at = renewing.value.map((c) => (c.renewAt ? Date.parse(c.renewAt) : Date.now())).sort((a, b) => a - b)[0]
  return at === undefined ? '' : intl.dateTime(Math.max(at, Date.now()))
})
const subtitle = computed(() => {
  const others = attention.value.length - renewing.value.length
  return renewing.value.length
    ? t('certs.overview.summary_renewing', { count: all.value.length, renewing: renewing.value.length, time: firstRenewal.value, attention: others })
    : t('certs.overview.summary', { count: all.value.length, attention: attention.value.length })
})

// What the buttons do: issue a certificate now, or deploy it again.
// Renewing certificates that work asks first.
const { issue, deploy, primary, ask } = useCertActions()
const asking = ref<CertAsk>()
const actionError = useApiError(computed(() => issue.error.value ?? deploy.error.value))
const busy = ref<number | null>(null)
async function act(run: () => Promise<unknown>, who: number) {
  busy.value = who
  try {
    await run()
  } catch {
    // shown above the summary
  } finally {
    busy.value = null
  }
}
function renewAll() {
  const certs = [...renewing.value]
  const [only] = certs
  asking.value =
    only && certs.length === 1 ? ask(only) : { kind: 'issue', certs, run: () => Promise.all(certs.map((c) => issue.mutateAsync(c.id))) }
}

// The summary's first button does the main thing for the certificate.
const selectedPrimary = computed(() => (selected.value ? primary(selected.value) : null))
function runPrimary(c: Certificate) {
  const main = primary(c)
  if (main?.confirm) asking.value = ask(c, main)
  else if (main?.run) void act(main.run, c.id)
}

const order: CertificateState[] = ['failed', 'deploy_failed', 'expired', 'expiring', 'issuing', 'deploying', 'ok']
const counts = computed(() =>
  order.map((state) => ({ state, count: all.value.filter((c) => c.state === state).length })).filter((s) => s.count > 0),
)

function lifeWords(l: Life | null): string {
  if (!l) return ''
  const text = lifeText(l)
  return t(text.key, text.params)
}

const day = 86_400_000

const errorText = (c: Certificate) =>
  c.issuance?.error ? t(c.issuance.error.key as MessageKey, (c.issuance.error.params ?? {}) as Params) : ''

/** plan says what happens next to c, or what stops it. */
function plan(c: Certificate, l: Life | null): string {
  if (c.state === 'deploy_failed') return deployNote(c)
  if (c.source === 'provider') {
    if (c.state === 'issuing') return t('certs.next.issuing')
    if (c.state === 'failed' && c.issuance?.state === 'pending' && c.issuance.nextAt) {
      return t('certs.next.retry', { time: intl.dateTime(c.issuance.nextAt), attempt: c.issuance.attempts + 1 })
    }
    if (c.state === 'failed') return t('certs.next.gave_up')
    if (!c.current) return t('certs.next.first')
    return t('certs.next.auto', { date: intl.date(Date.parse(c.current.notAfter) - c.renewBeforeDays * day) })
  }
  if (!l || l.expired) return t('certs.plan.expired')
  if (c.state === 'expiring' && c.current) return t('certs.plan.upload_due', { date: intl.date(c.current.notAfter) })
  return t('certs.plan.upload', { days: c.renewBeforeDays })
}

// The watched sites to look at, while their module is on: one row for all.
const manifest = useManifest()
const watching = computed(() => manifest.data.value?.modules.some((m) => m.id === 'watch' && m.enabled) ?? false)
const sites = useSites(watching)
const siteTodo = computed(() => (watching.value ? (sites.data.value ?? []).filter(needsLook) : []))
const siteNames = computed(() => {
  const names = siteTodo.value.map((s) => s.name || s.host)
  return names.length > 3 ? t('watch.overview.more', { names: names.slice(0, 3).join(', '), count: names.length - 3 }) : names.join(', ')
})

// What needs someone: a failed issuance or deployment shows its reason and
// leads to its log, with a button to try again; an uploaded certificate
// near its end asks for a new upload. Those that renew by themselves come
// as one, which needs no one.
const todo = computed(() =>
  attention.value.filter((c) => !renewing.value.includes(c)).map((c) => {
    const l = certLife(c)
    const domain = c.domains[0] ?? ''
    const expired = c.state === 'expired' || c.state === 'failed' || c.state === 'deploy_failed'
    const deployment = c.state === 'deploy_failed' ? failingDeployment(c) : undefined
    if (deployment) {
      return {
        cert: c,
        expired,
        title: t('certs.attention.deploy_failed', { domain }),
        detail: deployNote(c),
        to: `/certificates/${c.id}/deployments/${deployment.id}`,
        action: 'certs.detail.show_log' as MessageKey,
        button: { label: 'certs.action.deploy_now' as MessageKey, run: () => deploy.mutateAsync(c.id) },
      }
    }
    if (c.source === 'provider') {
      const failed = c.state === 'failed' && c.issuance
      return {
        cert: c,
        expired,
        title: t(failed ? 'certs.attention.failed' : expired ? 'certs.attention.expired' : 'certs.attention.renewing', { domain }),
        detail: failed ? `${errorText(c)} ${plan(c, l)}` : t('certs.note.renewing'),
        to: failed ? `/certificates/${c.id}/issuances/${c.issuance!.id}` : `/certificates/${c.id}`,
        action: (failed ? 'certs.detail.show_log' : 'certs.action.details') as MessageKey,
        button: failed ? { label: 'certs.action.retry' as MessageKey, run: () => issue.mutateAsync(c.id) } : undefined,
      }
    }
    return {
      cert: c,
      expired,
      title: t(expired ? 'certs.attention.expired' : 'certs.attention.replace', { domain }),
      detail: t(expired ? 'certs.attention.expired_detail' : 'certs.attention.replace_detail', { life: lifeWords(l) }),
      to: { path: `/certificates/${c.id}`, query: { upload: '1' } },
      action: 'certs.action.upload_short' as MessageKey,
      button: undefined,
    }
  }),
)

// The 90 days ahead, with a mark at each expiry and ticks every 30 days.
const horizon = 90
const ticks = computed(() => [30, 60, 90].map((d) => ({ left: `${(d / horizon) * 100}%`, date: intl.shortDate(Date.now() + d * day) })))
const upcoming = computed(() =>
  all.value
    .map((c) => ({ cert: c, life: certLife(c) }))
    .filter((x): x is { cert: Certificate; life: Life } => !!x.life && !x.life.expired && x.life.days <= horizon),
)
const marks = { ok: 'bg-ok-fill', warn: 'bg-warn-fill', danger: 'bg-danger-fill' }

// The last few issuances and deployments of the week.
const recent = useRuns(7)
const runText = useRunText()
const activity = computed(() => {
  const today = dayKey(Date.now())
  return [...(recent.data.value?.runs ?? [])]
    .sort((a, b) => Date.parse(runAt(b)) - Date.parse(runAt(a)))
    .map((r) => ({ run: r, result: runResult(r), text: runText.activity(r), at: runAt(r), link: runLink(r) }))
    .filter((a) => a.text)
    .slice(0, 5)
    .map((a) => ({ ...a, when: dayKey(a.at) === today ? intl.clock(a.at) : intl.shortDate(a.at) }))
})
const activityLooks = {
  ok: { icon: CircleCheck, ink: 'text-ok-ink' },
  failed: { icon: OctagonX, ink: 'text-danger-ink' },
  running: { icon: LoaderCircle, ink: 'text-info-ink' },
  canceled: { icon: CircleSlash, ink: 'text-ink-muted' },
} as const
const inks = { ok: 'text-ink-muted', warn: 'font-semibold text-warn-ink', danger: 'font-semibold text-danger-ink' }
</script>

<template>
  <PageHeader :title="t('kernel.overview.title')" :subtitle="all.length ? subtitle : undefined">
    <template #phone>
      <RouterLink
        to="/certificates"
        :aria-label="t('certs.search.label')"
        class="grid size-11 place-items-center rounded-md text-ink hover:bg-surface-sunken"
      >
        <Search class="size-4" aria-hidden="true" />
      </RouterLink>
      <RouterLink
        to="/certificates/new"
        :aria-label="t('certs.action.add')"
        class="grid size-11 place-items-center rounded-md bg-brand text-on-brand hover:bg-brand-hover hover:text-on-brand"
      >
        <Plus class="size-4" aria-hidden="true" />
      </RouterLink>
    </template>
    <template #actions>
      <SearchBox v-if="all.length" v-model="query" label="certs.search.label" class="w-60 max-w-full" />
      <JecButton variant="primary" to="/certificates/new"><Plus class="size-4" aria-hidden="true" />{{ t('certs.action.add') }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <JecAlert v-if="error" :code="error.code" :params="error.params" />
    <JecAlert v-if="actionError.error.value" :code="actionError.error.value.code" :params="actionError.error.value.params" />

    <section
      v-if="certificates.isSuccess.value && !all.length"
      class="flex flex-col items-start gap-3 rounded-lg border border-line bg-surface-raised p-4"
    >
      <p class="m-0 text-ink-muted">{{ t('certs.list.empty') }}</p>
      <JecButton variant="primary" to="/certificates/new"><Plus class="size-4" aria-hidden="true" />{{ t('certs.action.add') }}</JecButton>
    </section>

    <template v-else-if="all.length">
      <section aria-labelledby="inventory" class="flex flex-wrap gap-4 rounded-lg border border-line bg-surface-raised p-3 sm:p-4">
        <div class="flex min-w-0 flex-[999_1_420px] flex-col gap-3">
          <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
            <h2 id="inventory" class="m-0 text-heading font-semibold sm:text-section-title">{{ t('certs.overview.inventory') }}</h2>
            <span class="text-small text-ink-muted tabular-nums sm:hidden">
              {{ t('certs.overview.short_count', { count: all.length, attention: attention.length }) }}
            </span>
            <div class="hidden flex-wrap gap-1.5 sm:flex">
              <StatusBadge v-for="s in counts" :key="s.state" :state="s.state" :count="s.count" />
            </div>
          </div>
          <div role="list" :aria-label="t('certs.list.title')" class="flex flex-wrap gap-2">
            <div v-for="c in shown" :key="c.id" role="listitem">
              <CertSlot :cert="c" :pressed="selected?.id === c.id" @select="picked = c.id" />
            </div>
            <div role="listitem">
              <RouterLink
                to="/certificates/new"
                :aria-label="t('certs.action.add')"
                class="grid size-12 place-items-center rounded-sm text-ink-muted shadow-[inset_0_0_0_1px_var(--line-strong)] hover:bg-surface-sunken sm:size-14"
              >
                <Plus class="size-4" aria-hidden="true" />
              </RouterLink>
            </div>
          </div>
          <p class="m-0 hidden text-small text-ink-muted sm:block">{{ t('certs.overview.hint') }}</p>
        </div>

        <div
          v-if="selected"
          aria-live="polite"
          class="flex min-w-0 flex-[1_1_300px] flex-col gap-2.5 rounded-lg bg-surface-sunken p-3 sm:p-3.5"
        >
          <div class="flex min-w-0 items-center justify-between gap-2">
            <span class="min-w-0 truncate text-heading font-semibold" translate="no" :title="selected.domains[0]">
              {{ selected.domains[0] }}<small v-if="selectedExtra" class="ml-1 text-small font-normal text-ink-muted">{{ selectedExtra }}</small>
            </span>
            <StatusBadge :state="selected.state" />
          </div>
          <LifeBar v-if="selectedLife" :life="selectedLife" />
          <p
            v-if="selected.state === 'failed' && errorText(selected)"
            class="m-0 flex items-start gap-2 rounded-md bg-danger-soft px-2.5 py-2 text-small text-danger-ink [overflow-wrap:anywhere]"
          >
            <OctagonX class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ errorText(selected) }}
          </p>
          <dl class="m-0 hidden grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-small sm:grid">
            <dt class="text-ink-muted">{{ t('certs.field.source') }}</dt>
            <dd v-if="selected.source === 'provider'" class="m-0 min-w-0 truncate" translate="no" :title="sourceName(selected)">
              {{ sourceName(selected) }}
            </dd>
            <dd v-else class="m-0">{{ t('certs.source.upload') }}</dd>
            <dt class="text-ink-muted">{{ t('certs.field.expires') }}</dt>
            <dd class="m-0 tabular-nums">
              <time v-if="selected.current" :datetime="selected.current.notAfter">{{ intl.date(selected.current.notAfter) }}</time>
              <span v-else class="text-ink-muted">{{ t('certs.life.pending') }}</span>
            </dd>
            <dt class="text-ink-muted">{{ t('certs.field.targets') }}</dt>
            <dd class="m-0 flex min-w-0 flex-wrap gap-1.5">
              <span
                v-for="name in targetNames(selected)"
                :key="name"
                class="inline-flex h-[22px] max-w-full items-center truncate rounded-sm border border-line px-2 text-caption whitespace-nowrap text-ink-muted"
                translate="no"
                :title="name"
              >
                {{ name }}
              </span>
              <span v-if="!targetNames(selected).length" class="text-ink-muted">{{ t('certs.targets.none') }}</span>
            </dd>
          </dl>
          <p class="m-0 text-small text-ink-muted [overflow-wrap:anywhere]">{{ plan(selected, selectedLife) }}</p>
          <div class="mt-auto flex flex-wrap gap-2">
            <JecButton
              v-if="selected.source === 'upload'"
              variant="primary"
              size="sm"
              :to="{ path: `/certificates/${selected.id}`, query: { upload: '1' } }"
              class="max-sm:flex-1"
            >
              {{ t('certs.action.upload') }}
            </JecButton>
            <JecButton v-else-if="selectedPrimary?.to" variant="primary" size="sm" :to="selectedPrimary.to" class="max-sm:flex-1">
              {{ t(selectedPrimary.label) }}
            </JecButton>
            <JecButton
              v-else-if="selectedPrimary?.run"
              variant="primary"
              size="sm"
              :busy="busy === selected.id"
              class="max-sm:flex-1"
              @click="runPrimary(selected)"
            >
              {{ t(selectedPrimary.label) }}
            </JecButton>
            <JecButton size="sm" :to="`/certificates/${selected.id}`" class="max-sm:flex-1">{{ t('certs.action.details') }}</JecButton>
          </div>
        </div>
      </section>

      <div class="flex flex-wrap items-stretch gap-3 sm:gap-4">
        <section aria-labelledby="todo" class="flex min-w-0 flex-[1_1_300px] flex-col rounded-lg border border-line bg-surface-raised px-3 pt-3 pb-1 sm:p-4">
          <h2 id="todo" class="m-0 mb-0.5 text-heading font-semibold sm:text-section-title">{{ t('certs.attention.title') }}</h2>
          <p v-if="!todo.length && !renewing.length && !siteTodo.length" class="m-0 py-2.5 text-small text-ink-muted">{{ t('certs.attention.none') }}</p>
          <div v-if="renewing.length" class="flex items-start gap-2.5 border-b border-line py-2.5 last:border-b-0 last:pb-1">
            <TriangleAlert class="mt-[3px] size-4 flex-none text-warn-ink" aria-hidden="true" />
            <div class="min-w-0 flex-1">
              <div class="font-semibold [overflow-wrap:anywhere]">{{ t('certs.attention.renewal_group', { count: renewing.length }) }}</div>
              <div class="text-small text-ink-muted [overflow-wrap:anywhere]">{{ t('certs.attention.renewal_detail', { time: firstRenewal }) }}</div>
            </div>
            <JecButton variant="ghost" size="sm" @click="renewAll">{{ t('certs.action.renew_now') }}</JecButton>
          </div>
          <div
            v-for="item in todo"
            :key="item.cert.id"
            class="flex items-start gap-2.5 border-b border-line py-2.5 last:border-b-0 last:pb-1"
          >
            <CircleX v-if="item.expired" class="mt-[3px] size-4 flex-none text-danger-ink" aria-hidden="true" />
            <TriangleAlert v-else class="mt-[3px] size-4 flex-none text-warn-ink" aria-hidden="true" />
            <div class="min-w-0 flex-1">
              <div class="font-semibold [overflow-wrap:anywhere]">{{ item.title }}</div>
              <div class="text-small text-ink-muted [overflow-wrap:anywhere]">{{ item.detail }}</div>
            </div>
            <JecButton v-if="item.button" size="sm" :busy="busy === item.cert.id" @click="act(item.button.run, item.cert.id)">
              {{ t(item.button.label) }}
            </JecButton>
            <JecButton variant="ghost" size="sm" :to="item.to" class="max-sm:hidden">
              {{ t(item.action) }}
            </JecButton>
            <RouterLink
              :to="item.to"
              :aria-label="t(item.action)"
              class="-my-2.5 grid size-11 flex-none place-items-center text-ink-muted sm:hidden"
            >
              <ChevronRight class="size-4" aria-hidden="true" />
            </RouterLink>
          </div>
          <div v-if="siteTodo.length" class="flex items-start gap-2.5 border-b border-line py-2.5 last:border-b-0 last:pb-1">
            <TriangleAlert class="mt-[3px] size-4 flex-none text-warn-ink" aria-hidden="true" />
            <div class="min-w-0 flex-1">
              <div class="font-semibold [overflow-wrap:anywhere]">{{ t('watch.overview.title', { count: siteTodo.length }) }}</div>
              <div class="text-small text-ink-muted [overflow-wrap:anywhere]" translate="no">{{ siteNames }}</div>
            </div>
            <JecButton variant="ghost" size="sm" to="/sites" class="max-sm:hidden">{{ t('watch.overview.open') }}</JecButton>
            <RouterLink to="/sites" :aria-label="t('watch.overview.open')" class="-my-2.5 grid size-11 flex-none place-items-center text-ink-muted sm:hidden">
              <ChevronRight class="size-4" aria-hidden="true" />
            </RouterLink>
          </div>
        </section>

        <section aria-labelledby="timeline" class="hidden min-w-0 flex-[1_1_300px] flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4 sm:flex">
          <h2 id="timeline" class="m-0 text-section-title font-semibold">{{ t('certs.timeline.title') }}</h2>
          <div class="relative mx-1.5 h-[72px]">
            <div class="absolute inset-x-0 top-[30px] h-0.5 bg-line" />
            <div class="absolute top-6 left-0 h-3.5 w-0.5 bg-line-strong" />
            <div v-for="tick in ticks" :key="tick.left" class="absolute top-[26px] h-2.5 w-px bg-line-strong" :style="{ left: tick.left }" />
            <span
              v-for="u in upcoming"
              :key="u.cert.id"
              role="img"
              :aria-label="t('certs.timeline.marker', { domain: u.cert.domains[0] ?? '', date: intl.date(u.cert.current?.notAfter ?? 0) })"
              :title="t('certs.timeline.marker', { domain: u.cert.domains[0] ?? '', date: intl.date(u.cert.current?.notAfter ?? 0) })"
              :class="['absolute top-[25px] -ml-1.5 size-3 rounded-sm shadow-[0_0_0_2px_var(--surface-raised)]', marks[u.life.level]]"
              :style="{ left: `${(u.life.days / horizon) * 100}%` }"
            />
            <span class="absolute top-[50px] left-0 text-caption text-ink-muted">{{ t('certs.timeline.today') }}</span>
            <time
              v-for="(tick, i) in ticks"
              :key="tick.date"
              :class="['absolute top-[50px] text-caption whitespace-nowrap text-ink-muted tabular-nums', i === ticks.length - 1 ? 'right-0' : '-translate-x-1/2']"
              :style="i === ticks.length - 1 ? {} : { left: tick.left }"
            >{{ tick.date }}</time>
          </div>
          <p v-if="!upcoming.length" class="m-0 text-small text-ink-muted">{{ t('certs.timeline.none') }}</p>
          <ol class="m-0 flex list-none flex-col gap-1.5 p-0 text-small">
            <li v-for="u in upcoming" :key="u.cert.id" class="flex gap-3">
              <time :datetime="u.cert.current?.notAfter" :class="['w-[72px] flex-none tabular-nums', inks[u.life.level]]">
                {{ intl.shortDate(u.cert.current?.notAfter ?? 0) }}
              </time>
              <RouterLink :to="`/certificates/${u.cert.id}`" class="min-w-0 flex-1 truncate text-ink no-underline" translate="no" :title="u.cert.domains[0]">
                {{ u.cert.domains[0] }}
              </RouterLink>
              <span :class="['flex-none tabular-nums', inks[u.life.level]]">{{ lifeWords(u.life) }}</span>
            </li>
          </ol>
        </section>

        <section aria-labelledby="activity" class="flex min-w-0 flex-[1_1_300px] flex-col gap-0.5 rounded-lg border border-line bg-surface-raised px-3 pt-3 pb-1 sm:p-4">
          <div class="flex flex-wrap items-baseline justify-between gap-x-3">
            <h2 id="activity" class="m-0 text-heading font-semibold sm:text-section-title">{{ t('certs.activity.title') }}</h2>
            <RouterLink to="/history" class="text-small">{{ t('certs.activity.all') }}</RouterLink>
          </div>
          <p v-if="recent.isSuccess.value && !activity.length" class="m-0 py-2 text-small text-ink-muted">{{ t('certs.activity.none') }}</p>
          <ol class="m-0 flex list-none flex-col p-0 text-small">
            <li v-for="a in activity" :key="`${a.run.kind}-${a.run.id}`" class="flex items-start gap-2.5 border-b border-line py-2 last:border-b-0">
              <component :is="activityLooks[a.result].icon" :class="['mt-0.5 size-4 flex-none', activityLooks[a.result].ink]" aria-hidden="true" />
              <RouterLink :to="a.link" class="min-w-0 flex-1 text-ink no-underline [overflow-wrap:anywhere] hover:text-ink">{{ a.text }}</RouterLink>
              <time :datetime="a.at" class="flex-none text-ink-muted tabular-nums">{{ a.when }}</time>
            </li>
          </ol>
        </section>
      </div>
    </template>
  </div>

  <CertActionDialog v-model:ask="asking" />
</template>
