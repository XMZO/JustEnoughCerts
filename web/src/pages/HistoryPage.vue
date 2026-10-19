<script setup lang="ts">
import { CircleCheck, CircleSlash, Download, FileText, LoaderCircle, OctagonX } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { runsExport, runsPageSize, saveFile, useRunsPage, type Run, type RunsQuery } from '../api/certs'
import { useApiError } from '../api/errors'
import { actionText, resultText, runAt, runLink, runResult, runSeconds, triggerText, useRunText, type RunResult } from '../certs/runs'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecPager from '../components/JecPager.vue'
import PageHeader from '../components/PageHeader.vue'
import SearchBox from '../components/SearchBox.vue'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import { dayKey, stamp } from '../zone'

// Every issuance, renewal and deployment (docs/design/boards/History and
// HistoryMobile), the newest first and grouped by day, each leading to its
// log: filtered by result, action, time and domain, which the server does,
// and paged, as the user asked, the page in the address. On phones a list,
// from 640px a table.
const { t } = useT()
const intl = useIntl()
const text = useRunText()

const ranges = [
  { days: 7, label: 'certs.runs.range.days_7' },
  { days: 30, label: 'certs.runs.range.days_30' },
  { days: 90, label: 'certs.runs.range.days_90' },
  { days: 0, label: 'certs.runs.range.all' },
] as const
const days = ref(30)

const actions = [
  { id: 'all', label: 'certs.runs.action.all' },
  { id: 'issue', label: 'certs.runs.action.issue' },
  { id: 'renew', label: 'certs.runs.action.renew' },
  { id: 'deploy', label: 'certs.runs.action.deploy' },
] as const
const action = ref<'all' | Run['action']>('all')
type Filter = 'all' | RunResult
const filter = ref<Filter>('all')

// The domain looked for, once typing pauses.
const query = ref('')
const domain = ref('')
let typing: ReturnType<typeof setTimeout> | undefined
watch(query, (q) => {
  clearTimeout(typing)
  typing = setTimeout(() => (domain.value = q.trim()), 300)
})

// The page, in the address: back and forward keep it; another filter
// starts over at the first.
const route = useRoute()
const router = useRouter()
const page = computed({
  get: () => Math.max(1, Number(route.query.page) || 1),
  set: (p: number) => {
    void router.replace({ query: { ...route.query, page: p > 1 ? String(p) : undefined } })
    window.scrollTo({ top: 0 })
  },
})
watch([days, action, filter, domain], () => {
  if (page.value > 1) page.value = 1
})
const asked = computed<RunsQuery>(() => ({
  days: days.value,
  action: action.value === 'all' ? undefined : action.value,
  result: filter.value === 'all' ? undefined : filter.value,
  q: domain.value || undefined,
}))
const runs = useRunsPage(() => ({ ...asked.value, page: page.value }))
const loadError = useApiError(runs.error)
const shown = computed(() => runs.data.value?.runs ?? [])
const total = computed(() => runs.data.value?.total ?? 0)
const pages = computed(() => Math.max(1, Math.ceil(total.value / runsPageSize)))
// Past the last page, as after a filter: the last one.
watch(pages, (n) => {
  if (page.value > n) page.value = n
})
const filters = computed(() => {
  const counts = runs.data.value?.counts
  return (['all', 'ok', 'failed', 'running'] as const).map((id): { id: Filter; label: MessageKey; count: number } => ({
    id,
    label: id === 'all' ? 'certs.runs.result.all' : resultText[id],
    count: counts ? counts[id] : 0,
  }))
})

interface Row {
  run: Run
  result: RunResult
  at: string
  note: string
  duration: string
  link: string
}
interface Group {
  key: string
  /** Today or yesterday, as a message; otherwise the date, which Intl writes. */
  label: string
  date: boolean
  rows: Row[]
}
const day = 86_400_000
const groups = computed(() => {
  const today = dayKey(Date.now())
  const yesterday = dayKey(Date.now() - day)
  const out: Group[] = []
  for (const r of shown.value) {
    const at = runAt(r)
    const key = dayKey(at)
    let g = out[out.length - 1]
    if (!g || g.key !== key) {
      const relative = key === today ? 'certs.runs.today' : key === yesterday ? 'certs.runs.yesterday' : null
      g = { key, label: relative ? t(relative) : intl.date(at), date: !relative, rows: [] }
      out.push(g)
    }
    g.rows.push({ run: r, result: runResult(r), at, note: text.note(r), duration: text.duration(r), link: runLink(r) })
  }
  return out
})

const looks = {
  ok: { icon: CircleCheck, badge: 'bg-ok-soft text-ok-ink', ink: 'text-ok-ink' },
  failed: { icon: OctagonX, badge: 'bg-danger-soft text-danger-ink', ink: 'text-danger-ink' },
  running: { icon: LoaderCircle, badge: 'bg-info-soft text-info-ink', ink: 'text-info-ink', spin: true },
  canceled: { icon: CircleSlash, badge: 'bg-surface-sunken text-ink-muted', ink: 'text-ink-muted' },
} as const

const columns = [
  'certs.runs.column.time',
  'certs.runs.column.cert',
  'certs.runs.column.action',
  'certs.runs.column.result',
  'certs.runs.column.note',
  'certs.runs.column.duration',
] as const

// The runs the filters match, every page, as CSV: times in the panel's
// zone, and a byte order mark so that spreadsheets read the text as UTF-8.
const cell = (v: string) => (/[",\r\n]/.test(v) ? `"${v.replaceAll('"', '""')}"` : v)
const exporting = ref(false)
async function exportCsv() {
  exporting.value = true
  let list: Run[]
  try {
    list = await runsExport(asked.value)
  } catch {
    return // the page shows why the history cannot be read
  } finally {
    exporting.value = false
  }
  const lines = [columns.map((c) => t(c))]
  for (const r of list) {
    lines.push([
      stamp(runAt(r)),
      r.domain,
      `${t(actionText[r.action])} ${t(triggerText[r.trigger])}`,
      t(resultText[runResult(r)]),
      text.note(r),
      String(runSeconds(r) ?? ''),
    ])
  }
  const csv = String.fromCharCode(0xfeff) + lines.map((l) => l.map(cell).join(',')).join('\r\n') + '\r\n'
  saveFile(new Blob([csv], { type: 'text/csv;charset=utf-8' }), `jec-history-${dayKey(Date.now()).replaceAll('-', '')}.csv`)
}

const selectClass =
  'h-9 rounded-md border border-line-strong bg-surface-raised px-2 text-small text-ink max-sm:h-11 max-sm:min-w-0 max-sm:flex-1'
</script>

<template>
  <PageHeader :title="t('certs.runs.title')" :subtitle="t('certs.runs.intro')">
    <template #actions>
      <JecButton :disabled="!shown.length" :busy="exporting" @click="exportCsv"><Download class="size-4" aria-hidden="true" />{{ t('certs.runs.export') }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t('certs.runs.intro') }}</p>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />

    <div class="flex flex-wrap items-center gap-2">
      <SearchBox v-model="query" label="certs.search.label" class="w-full sm:max-w-72 sm:flex-[1_1_220px]" />
      <div role="group" :aria-label="t('certs.runs.filter')" class="flex flex-wrap gap-2 sm:gap-1.5">
        <button
          v-for="f in filters"
          :key="f.id"
          type="button"
          :aria-pressed="filter === f.id ? 'true' : 'false'"
          :class="[
            'inline-flex cursor-pointer items-center gap-1.5 border font-semibold whitespace-nowrap text-ink',
            'h-9 rounded-full px-3.5 text-small sm:h-[30px] sm:rounded-md sm:px-3',
            filter === f.id ? 'border-brand bg-brand-soft' : 'border-line-strong bg-surface-raised hover:bg-surface-sunken',
          ]"
          @click="filter = f.id"
        >
          {{ t(f.label) }}<data :value="f.count" class="font-medium text-ink-muted tabular-nums">{{ f.count }}</data>
        </button>
      </div>
      <div class="flex w-full gap-2 sm:w-auto">
        <select v-model="action" :aria-label="t('certs.runs.column.action')" :class="selectClass">
          <option v-for="a in actions" :key="a.id" :value="a.id">{{ t(a.label) }}</option>
        </select>
        <select v-model.number="days" :aria-label="t('certs.runs.range.label')" :class="selectClass">
          <option v-for="r in ranges" :key="r.days" :value="r.days">{{ t(r.label) }}</option>
        </select>
      </div>
    </div>

    <p v-if="runs.isSuccess.value && !runs.data.value?.counts.all && !domain" class="m-0 rounded-lg border border-line bg-surface-raised p-4 text-ink-muted">
      {{ t('certs.runs.empty') }}
    </p>
    <p v-else-if="runs.isSuccess.value && !shown.length" class="m-0 rounded-lg border border-line bg-surface-raised p-4 text-ink-muted">
      {{ t('certs.runs.no_match') }}
    </p>

    <!-- From 640px: the table, a group of rows for each day. -->
    <div v-if="shown.length" class="hidden overflow-x-auto sm:block">
      <table class="w-full border-separate border-spacing-0 overflow-hidden rounded-lg border border-line bg-surface-raised text-small">
        <thead>
          <tr>
            <th
              v-for="col in columns"
              :key="col"
              scope="col"
              :class="[
                'border-b border-line bg-surface-sunken px-3 py-2 text-left font-semibold whitespace-nowrap text-ink-muted',
                col === 'certs.runs.column.duration' && 'text-right',
              ]"
            >
              {{ t(col) }}
            </th>
            <th scope="col" class="border-b border-line bg-surface-sunken px-3 py-2"><span class="sr-only">{{ t('certs.detail.show_log') }}</span></th>
          </tr>
        </thead>
        <tbody v-for="g in groups" :key="g.key">
          <tr>
            <th colspan="7" scope="rowgroup" class="border-b border-line px-3 pt-2.5 pb-1 text-left font-semibold text-ink">
              <time v-if="g.date" :datetime="g.rows[0]?.at">{{ g.label }}</time>
              <template v-else>{{ g.label }}</template>
            </th>
          </tr>
          <tr v-for="row in g.rows" :key="`${row.run.kind}-${row.run.id}`" class="hover:bg-surface-sunken">
            <td class="w-16 border-b border-line px-3 py-1.5 text-ink-muted tabular-nums">
              <time :datetime="row.at">{{ intl.clock(row.at) }}</time>
            </td>
            <td class="min-w-48 border-b border-line px-3 py-1.5 [overflow-wrap:anywhere]">
              <RouterLink :to="`/certificates/${row.run.certificate}`" class="font-semibold text-ink no-underline hover:text-ink" translate="no">
                {{ row.run.domain }}
              </RouterLink>
            </td>
            <td class="border-b border-line px-3 py-1.5 whitespace-nowrap">
              {{ t(actionText[row.run.action]) }}<span class="ml-1.5 text-caption text-ink-muted">{{ t(triggerText[row.run.trigger]) }}</span>
            </td>
            <td class="border-b border-line px-3 py-1.5">
              <span :class="['inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-semibold whitespace-nowrap', looks[row.result].badge]">
                <component :is="looks[row.result].icon" :class="['size-3.5', 'spin' in looks[row.result] && 'animate-spin']" aria-hidden="true" />
                {{ t(resultText[row.result]) }}
              </span>
            </td>
            <!-- Takes the room left, and cuts what does not fit. -->
            <td class="w-full max-w-0 truncate border-b border-line px-3 py-1.5 text-ink-muted" :title="row.note">{{ row.note }}</td>
            <td class="border-b border-line px-3 py-1.5 text-right whitespace-nowrap text-ink-muted tabular-nums">{{ row.duration }}</td>
            <td class="w-11 border-b border-line px-1 py-0.5">
              <RouterLink
                :to="row.link"
                :aria-label="t('certs.detail.show_log')"
                :title="t('certs.detail.show_log')"
                class="grid size-9 place-items-center rounded-md text-ink-muted hover:bg-surface-sunken hover:text-ink"
              >
                <FileText class="size-4" aria-hidden="true" />
              </RouterLink>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Phones: a list for each day. -->
    <section v-for="g in groups" :key="g.key" :aria-label="g.label" class="grid gap-2 sm:hidden">
      <h2 class="m-0 px-1 text-small font-semibold text-ink-muted">
        <time v-if="g.date" :datetime="g.rows[0]?.at">{{ g.label }}</time>
        <template v-else>{{ g.label }}</template>
      </h2>
      <div role="list" class="overflow-hidden rounded-lg border border-line bg-surface-raised">
        <div v-for="row in g.rows" :key="`${row.run.kind}-${row.run.id}`" role="listitem" class="border-b border-line last:border-b-0">
          <RouterLink
            :to="row.link"
            class="grid min-h-14 grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 px-4 py-2.5 text-ink no-underline hover:bg-surface-sunken hover:text-ink"
          >
            <component
              :is="looks[row.result].icon"
              :class="['mt-0.5 size-5', looks[row.result].ink, 'spin' in looks[row.result] && 'animate-spin']"
              aria-hidden="true"
            />
            <span class="grid min-w-0 gap-0.5">
              <span class="truncate font-semibold" translate="no" :title="row.run.domain">{{ row.run.domain }}</span>
              <span class="flex flex-wrap gap-x-2 text-small">
                <span>{{ t(actionText[row.run.action]) }}</span>
                <span :class="['font-semibold', looks[row.result].ink]">{{ t(resultText[row.result]) }}</span>
              </span>
              <span v-if="row.note" class="truncate text-small text-ink-muted" :title="row.note">{{ row.note }}</span>
            </span>
            <time :datetime="row.at" class="text-small text-ink-muted tabular-nums">{{ intl.clock(row.at) }}</time>
          </RouterLink>
        </div>
      </div>
    </section>

    <JecPager v-model="page" :pages="pages" />
    <p v-if="shown.length" class="m-0 text-center text-small text-ink-muted">
      {{ t('kernel.pager.summary', { from: (page - 1) * runsPageSize + 1, to: (page - 1) * runsPageSize + shown.length, total }) }}
    </p>
  </div>
</template>
