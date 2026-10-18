<script setup lang="ts">
import { CircleCheck, Download, KeyRound, OctagonX } from '@lucide/vue'
import { computed, ref } from 'vue'

import { useAuditEntries, type AuditEntry } from '../api/audit'
import { saveFile } from '../api/certs'
import { useApiError } from '../api/errors'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import PageHeader from '../components/PageHeader.vue'
import SearchBox from '../components/SearchBox.vue'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'

// What was done through the API (module audit), the newest first and
// grouped by day, filtered by result, time and text instead of paged, as
// the history is (docs/design/boards/History). On phones a list, from
// 640px a table.
const { t } = useT()
const intl = useIntl()

const ranges = [
  { days: 7, label: 'audit.range.days_7' },
  { days: 30, label: 'audit.range.days_30' },
  { days: 90, label: 'audit.range.days_90' },
  { days: 0, label: 'audit.range.all' },
] as const
const days = ref(30)
const entries = useAuditEntries(days)
const loadError = useApiError(entries.error)

// What the operations are called; the others show as their method and path.
const operations: Partial<Record<string, MessageKey>> = {
  setup: 'audit.op.setup',
  'sign-in': 'audit.op.sign_in',
  'sign-out': 'audit.op.sign_out',
  'update-account': 'audit.op.update_account',
  'change-password': 'audit.op.change_password',
  'two-factor-secret': 'audit.op.two_factor_secret',
  'two-factor-enable': 'audit.op.two_factor_enable',
  'two-factor-disable': 'audit.op.two_factor_disable',
  backup: 'audit.op.backup',
  'config-export-secrets': 'audit.op.config_export_secrets',
  'config-check': 'audit.op.config_check',
  'config-import': 'audit.op.config_import',
  'switch-module': 'audit.op.switch_module',
  'providers-create': 'audit.op.providers_create',
  'providers-update': 'audit.op.providers_update',
  'providers-delete': 'audit.op.providers_delete',
  'providers-test': 'audit.op.providers_test',
  'credentials-create': 'audit.op.credentials_create',
  'credentials-update': 'audit.op.credentials_update',
  'credentials-delete': 'audit.op.credentials_delete',
  'certs-add': 'audit.op.certs_add',
  'certs-issue-new': 'audit.op.certs_issue_new',
  'certs-update': 'audit.op.certs_update',
  'certs-delete': 'audit.op.certs_delete',
  'certs-upload': 'audit.op.certs_upload',
  'certs-download': 'audit.op.certs_download',
  'certs-issue': 'audit.op.certs_issue',
  'certs-deploy': 'audit.op.certs_deploy',
  'certs-save-settings': 'audit.op.certs_save_settings',
  'monitor-check': 'audit.op.monitor_check',
  'notify-test': 'audit.op.notify_test',
  'tokens-create': 'audit.op.tokens_create',
  'tokens-delete': 'audit.op.tokens_delete',
  'oidc-save': 'audit.op.oidc_save',
  'oidc-callback': 'audit.op.oidc_callback',
}
// The operations that hand out secrets: private keys, backups and the
// configuration with its secrets.
const secret = new Set(['certs-download', 'backup', 'config-export-secrets'])
const raw = (e: AuditEntry) => `${e.method} ${e.path}`
const label = (e: AuditEntry) => {
  const key = operations[e.operation]
  return key ? t(key) : raw(e)
}

type Result = 'ok' | 'failed'
const resultOf = (e: AuditEntry): Result => (e.status < 400 ? 'ok' : 'failed')
const resultText: Record<Result, MessageKey> = { ok: 'audit.result.ok', failed: 'audit.result.failed' }
const looks = {
  ok: { icon: CircleCheck, badge: 'bg-ok-soft text-ok-ink', ink: 'text-ok-ink' },
  failed: { icon: OctagonX, badge: 'bg-danger-soft text-danger-ink', ink: 'text-danger-ink' },
} as const

const query = ref('')
type Filter = 'all' | Result
const filter = ref<Filter>('all')
const all = computed(() => entries.data.value?.entries ?? [])
const searched = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return all.value
  return all.value.filter((e) => [label(e), e.path, e.user ?? '', e.client].some((s) => s.toLowerCase().includes(q)))
})
const filters = computed(() =>
  (['all', 'ok', 'failed'] as const).map((id): { id: Filter; label: MessageKey; count: number } => ({
    id,
    label: id === 'all' ? 'audit.result.all' : resultText[id],
    count: id === 'all' ? searched.value.length : searched.value.filter((e) => resultOf(e) === id).length,
  })),
)
const shown = computed(() => searched.value.filter((e) => filter.value === 'all' || resultOf(e) === filter.value))

interface Group {
  key: string
  /** Today or yesterday, as a message; otherwise the date, which Intl writes. */
  label: string
  date: boolean
  entries: AuditEntry[]
}
const day = 86_400_000
const groups = computed(() => {
  const today = new Date().toDateString()
  const yesterday = new Date(Date.now() - day).toDateString()
  const out: Group[] = []
  for (const e of shown.value) {
    const key = new Date(e.at).toDateString()
    let g = out[out.length - 1]
    if (!g || g.key !== key) {
      const relative = key === today ? 'audit.page.today' : key === yesterday ? 'audit.page.yesterday' : null
      g = { key, label: relative ? t(relative) : intl.date(e.at), date: !relative, entries: [] }
      out.push(g)
    }
    g.entries.push(e)
  }
  return out
})

const columns = ['audit.column.time', 'audit.column.action', 'audit.column.account', 'audit.column.result', 'audit.column.client'] as const

// The entries shown, as CSV: local times, and a byte order mark so that
// spreadsheets read the text as UTF-8.
const pad = (n: number) => String(n).padStart(2, '0')
function local(at: string): string {
  const d = new Date(at)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}
const cell = (v: string) => (/[",\r\n]/.test(v) ? `"${v.replaceAll('"', '""')}"` : v)
function exportCsv() {
  const lines = [[...columns.map((c) => t(c)), 'HTTP', 'User-Agent']]
  for (const e of shown.value) {
    lines.push([local(e.at), `${label(e)} (${raw(e)})`, e.user ?? '', t(resultText[resultOf(e)]), e.client, String(e.status), e.userAgent ?? ''])
  }
  const csv = String.fromCharCode(0xfeff) + lines.map((l) => l.map(cell).join(',')).join('\r\n') + '\r\n'
  const now = new Date()
  saveFile(new Blob([csv], { type: 'text/csv;charset=utf-8' }), `jec-audit-${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}.csv`)
}

const selectClass =
  'h-9 rounded-md border border-line-strong bg-surface-raised px-2 text-small text-ink max-sm:h-11 max-sm:min-w-0 max-sm:flex-1'
</script>

<template>
  <PageHeader :title="t('audit.page.title')" :subtitle="t('audit.page.intro')">
    <template #actions>
      <JecButton :disabled="!shown.length" @click="exportCsv"><Download class="size-4" aria-hidden="true" />{{ t('audit.page.export') }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t('audit.page.intro') }}</p>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />

    <div class="flex flex-wrap items-center gap-2">
      <SearchBox v-model="query" label="audit.page.search" class="w-full sm:max-w-72 sm:flex-[1_1_220px]" />
      <div role="group" :aria-label="t('audit.page.filter')" class="flex flex-wrap gap-2 sm:gap-1.5">
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
        <select v-model.number="days" :aria-label="t('audit.range.label')" :class="selectClass">
          <option v-for="r in ranges" :key="r.days" :value="r.days">{{ t(r.label) }}</option>
        </select>
      </div>
    </div>

    <p v-if="entries.isSuccess.value && !all.length" class="m-0 rounded-lg border border-line bg-surface-raised p-4 text-ink-muted">
      {{ t('audit.page.empty') }}
    </p>
    <p v-else-if="entries.isSuccess.value && !shown.length" class="m-0 rounded-lg border border-line bg-surface-raised p-4 text-ink-muted">
      {{ t('audit.page.no_match') }}
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
              class="border-b border-line bg-surface-sunken px-3 py-2 text-left font-semibold whitespace-nowrap text-ink-muted"
            >
              {{ t(col) }}
            </th>
          </tr>
        </thead>
        <tbody v-for="g in groups" :key="g.key">
          <tr>
            <th colspan="5" scope="rowgroup" class="border-b border-line px-3 pt-2.5 pb-1 text-left font-semibold text-ink">
              <time v-if="g.date" :datetime="g.entries[0]?.at">{{ g.label }}</time>
              <template v-else>{{ g.label }}</template>
            </th>
          </tr>
          <tr v-for="e in g.entries" :key="e.id" class="hover:bg-surface-sunken">
            <td class="w-20 border-b border-line px-3 py-1.5 align-top text-ink-muted tabular-nums">
              <time :datetime="e.at">{{ intl.clock(e.at) }}</time>
            </td>
            <td class="w-full border-b border-line px-3 py-1.5 align-top [overflow-wrap:anywhere]">
              <span class="flex items-center gap-1.5 font-semibold">
                <KeyRound v-if="secret.has(e.operation)" class="size-3.5 shrink-0 text-warn-ink" aria-hidden="true" />
                <template v-if="operations[e.operation]">{{ label(e) }}</template>
                <code v-else>{{ raw(e) }}</code>
              </span>
              <code v-if="operations[e.operation]" class="text-caption text-ink-muted">{{ e.path }}</code>
            </td>
            <td class="border-b border-line px-3 py-1.5 align-top whitespace-nowrap">
              <span v-if="e.user" translate="no">{{ e.user }}</span>
              <span v-else class="text-ink-muted">{{ t('audit.page.no_account') }}</span>
              <span v-if="e.token" class="block text-caption text-ink-muted">{{ t('audit.page.via_token', { name: e.token }) }}</span>
            </td>
            <td class="border-b border-line px-3 py-1.5 align-top">
              <span
                :class="['inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-semibold whitespace-nowrap', looks[resultOf(e)].badge]"
                :title="t('audit.result.status', { status: e.status })"
              >
                <component :is="looks[resultOf(e)].icon" class="size-3.5" aria-hidden="true" />
                {{ t(resultText[resultOf(e)]) }}
              </span>
            </td>
            <td class="border-b border-line px-3 py-1.5 align-top whitespace-nowrap text-ink-muted tabular-nums">
              <span translate="no" :title="e.userAgent">{{ e.client }}</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Phones: a list for each day. -->
    <section v-for="g in groups" :key="g.key" :aria-label="g.label" class="grid gap-2 sm:hidden">
      <h2 class="m-0 px-1 text-small font-semibold text-ink-muted">
        <time v-if="g.date" :datetime="g.entries[0]?.at">{{ g.label }}</time>
        <template v-else>{{ g.label }}</template>
      </h2>
      <ul class="m-0 list-none overflow-hidden rounded-lg border border-line bg-surface-raised p-0">
        <li
          v-for="e in g.entries"
          :key="e.id"
          class="grid min-h-14 grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 border-b border-line px-4 py-2.5 last:border-b-0"
        >
          <component :is="looks[resultOf(e)].icon" :class="['mt-0.5 size-5', looks[resultOf(e)].ink]" aria-hidden="true" />
          <span class="grid min-w-0 gap-0.5">
            <span class="flex min-w-0 items-center gap-1.5 font-semibold [overflow-wrap:anywhere]">
              <KeyRound v-if="secret.has(e.operation)" class="size-3.5 shrink-0 text-warn-ink" aria-hidden="true" />
              <template v-if="operations[e.operation]">{{ label(e) }}</template>
              <code v-else>{{ raw(e) }}</code>
            </span>
            <span class="flex flex-wrap gap-x-2 text-small">
              <span v-if="e.user" translate="no">{{ e.user }}</span>
              <span v-else class="text-ink-muted">{{ t('audit.page.no_account') }}</span>
              <span v-if="e.token" class="text-ink-muted">{{ t('audit.page.via_token', { name: e.token }) }}</span>
              <span translate="no" class="text-ink-muted">{{ e.client }}</span>
              <span :class="['font-semibold', looks[resultOf(e)].ink]">{{ t(resultText[resultOf(e)]) }}</span>
            </span>
          </span>
          <time :datetime="e.at" class="text-small text-ink-muted tabular-nums">{{ intl.clock(e.at) }}</time>
        </li>
      </ul>
    </section>

    <p v-if="shown.length" class="m-0 text-small text-ink-muted">
      {{ t('audit.page.summary', { shown: shown.length, total: all.length }) }}
      <template v-if="entries.data.value?.more">{{ t('audit.page.more', { count: all.length }) }}</template>
    </p>
  </div>
</template>
