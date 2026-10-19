<script setup lang="ts">
import { Activity, CircleCheck, CircleX, Eye, FileDown, LoaderCircle, OctagonX, Plus, RefreshCw, Send, Trash2, TriangleAlert, Upload } from '@lucide/vue'
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { ApiError, networkError } from '../api/client'
import { useCertificates, useDeleteCertificate, type Certificate } from '../api/certs'
import { useApiError } from '../api/errors'
import { useMonitorStates } from '../api/monitor'
import { useProviders } from '../api/providers'
import { useCertActions } from '../certs/actions'
import { useDeployNote } from '../certs/deployment'
import { barPercent, certLife, lifeText, needsAttention, stateText } from '../certs/life'
import CertActionDialog, { type CertAsk } from '../components/CertActionDialog.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import LifeBar from '../components/LifeBar.vue'
import PageHeader from '../components/PageHeader.vue'
import RowMenu, { type MenuItem } from '../components/RowMenu.vue'
import SearchBox from '../components/SearchBox.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'

// The certificate list (docs/design/boards/Certs and CertsMobile): a
// compact table from 640px; on phones the certificates that need attention
// as rows that say why, and the healthy ones as tiles two to a row.
const { t } = useT()
const intl = useIntl()
const certificates = useCertificates()
const sources = useProviders('source')
const targets = useProviders('target')
const deployNote = useDeployNote()

const error = computed(() => {
  const err = certificates.error.value
  if (!err) return null
  return err instanceof ApiError ? err : networkError
})

// Domains that still serve an older certificate, as the monitor found,
// need attention too.
const monitorStates = useMonitorStates()
const servedOld = (c: Certificate) => (monitorStates.data.value?.get(c.id)?.old ?? 0) > 0
const attention = (c: Certificate) => needsAttention(c) || servedOld(c)

type Filter = 'all' | 'attention' | 'ok'
const filter = ref<Filter>('all')
const query = ref('')
// The source of the certificates shown: '' for all, upload, or the ID of a
// configured one.
const fromSource = ref('')

const all = computed(() => certificates.data.value ?? [])
const searched = computed(() => {
  const q = query.value.trim().toLowerCase()
  return all.value
    .filter((c) => !q || c.domains.some((d) => d.includes(q)))
    .filter((c) => !fromSource.value || (fromSource.value === 'upload' ? c.source === 'upload' : String(c.sourceProvider) === fromSource.value))
})
// The sources there are certificates of, to choose from.
const sourceChoices = computed(() => [
  ...(sources.data.value ?? []).filter((p) => all.value.some((c) => c.sourceProvider === p.id)).map((p) => ({ value: String(p.id), name: p.name })),
])
const hasUploads = computed(() => all.value.some((c) => c.source === 'upload'))
const shown = computed(() =>
  searched.value.filter((c) => filter.value === 'all' || (filter.value === 'attention') === attention(c)),
)
const filters = computed<{ id: Filter; label: MessageKey; count: number }[]>(() => [
  { id: 'all', label: 'certs.filter.all', count: searched.value.length },
  { id: 'attention', label: 'certs.attention.title', count: searched.value.filter(attention).length },
  { id: 'ok', label: 'certs.state.ok', count: searched.value.filter((c) => !attention(c)).length },
])

interface Row {
  cert: Certificate
  domain: string
  extra: string
  life: ReturnType<typeof certLife>
  lifeWords: string
  label: string
  note: string
  /** Where new versions come from: a configured source by its name, or uploads. */
  source: string
  /** The names of the targets it deploys to. */
  targets: string[]
  /** What happens next: the renewal, or the upload someone has to do. */
  next: string
}

const day = 86_400_000

/** note says why a certificate needs attention. */
function note(c: Certificate): string {
  if (servedOld(c) && (c.state === 'ok' || c.state === 'deploying')) return t('monitor.note.served_old')
  if (c.state === 'failed') return issueNote(c)
  if (c.state === 'deploy_failed') return deployNote(c)
  if (c.state === 'ok' || c.state === 'issuing' || c.state === 'deploying') return ''
  if (c.source === 'provider') {
    // Waiting for the check time, or under way already.
    return c.renewAt && Date.parse(c.renewAt) > Date.now() ? t('certs.next.auto', { date: intl.dateTime(c.renewAt) }) : t('certs.note.renewing')
  }
  return t(c.state === 'expired' ? 'certs.note.expired' : 'certs.note.manual')
}

/** issueNote says why the latest issuance of a certificate failed. */
function issueNote(c: Certificate): string {
  const e = c.issuance?.error
  return e ? t(e.key as MessageKey, (e.params ?? {}) as Params) : ''
}

/** next says what happens to a certificate next, or what stops it. */
function next(c: Certificate): string {
  if (c.state === 'deploy_failed') return deployNote(c)
  if (c.source !== 'provider') return t('certs.next.manual')
  if (c.state === 'issuing') return t('certs.next.issuing')
  if (c.state === 'failed') return issueNote(c)
  if (!c.current) return t('certs.next.first')
  return t('certs.next.auto', { date: c.renewAt ? intl.dateTime(c.renewAt) : intl.date(Date.parse(c.current.notAfter) - c.renewBeforeDays * day) })
}

const rows = computed<Row[]>(() =>
  shown.value.map((c) => {
    const l = certLife(c)
    const text = l ? lifeText(l) : null
    const lifeWords = text ? t(text.key, text.params) : ''
    const domain = c.domains[0] ?? ''
    return {
      cert: c,
      domain,
      extra: c.domains.length > 1 ? `+${c.domains.length - 1}` : '',
      life: l,
      lifeWords,
      label: t('certs.cert.label', { domain, state: t(stateText[c.state]), life: lifeWords }),
      note: note(c),
      source: c.source === 'provider' ? (sources.data.value?.find((p) => p.id === c.sourceProvider)?.name ?? '') : t('certs.source.upload'),
      targets: c.targets.map((id) => targets.data.value?.find((p) => p.id === id)?.name ?? '').filter(Boolean),
      next: next(c),
    }
  }),
)

// The menu of each row of the table: the details, the main thing to do,
// deploying again, a new version of an upload, and deletion. Renewing a
// certificate that works, deploying it again and deleting it ask first.
const { primary, ask, askDeploy } = useCertActions()
const asking = ref<CertAsk>()
// What went wrong with the last action, above the table.
const failure = ref<unknown>()
const actionError = useApiError(failure)
async function act(run: () => Promise<unknown>) {
  failure.value = undefined
  try {
    await run()
  } catch (err) {
    failure.value = err
  }
}
function menu(c: Certificate): MenuItem[] {
  const items: MenuItem[] = [{ key: 'details', label: t('certs.action.details'), icon: Eye, to: `/certificates/${c.id}` }]
  const main = primary(c)
  if (main) {
    const run = main.run
    items.push({
      key: 'main',
      label: t(main.label),
      hint: main.hint && t(main.hint),
      icon: main.to ? Activity : RefreshCw,
      to: main.to,
      run: run && (() => (main.confirm ? (asking.value = ask(c, main)) : void act(run))),
    })
  }
  if (c.current && c.targets.length && c.state !== 'deploy_failed' && c.state !== 'deploying') {
    items.push({ key: 'deploy', label: t('certs.action.redeploy'), hint: t('certs.menu.redeploy_hint'), icon: Send, run: () => (asking.value = askDeploy(c)) })
  }
  if (c.source !== 'provider') items.push({ key: 'upload', label: t('certs.action.upload'), icon: Upload, to: `/certificates/${c.id}?upload=1` })
  items.push({ key: 'delete', label: t('certs.action.delete'), icon: Trash2, danger: true, run: () => (deleting.value = c) })
  return items
}
const deleting = ref<Certificate>()
const deleteOpen = computed({
  get: () => !!deleting.value,
  set: (open) => {
    if (!open) deleting.value = undefined
  },
})
const remove = useDeleteCertificate(() => deleting.value?.id ?? 0)
const removeError = useApiError(remove.error)
async function confirmDelete() {
  try {
    await remove.mutateAsync(undefined)
  } catch {
    return // shown in the dialog
  }
  deleting.value = undefined
}

const attentionRows = computed(() => rows.value.filter((r) => attention(r.cert)))
const healthyRows = computed(() => rows.value.filter((r) => !attention(r.cert)))

const itemLooks = {
  expiring: { icon: TriangleAlert, ink: 'text-warn-ink', fill: 'bg-warn-fill' },
  expired: { icon: CircleX, ink: 'text-danger-ink', fill: 'bg-danger-fill' },
  failed: { icon: OctagonX, ink: 'text-danger-ink', fill: 'bg-danger-fill' },
  deploy_failed: { icon: OctagonX, ink: 'text-danger-ink', fill: 'bg-danger-fill' },
  issuing: { icon: LoaderCircle, ink: 'text-info-ink', fill: 'bg-info-fill' },
  deploying: { icon: LoaderCircle, ink: 'text-info-ink', fill: 'bg-info-fill' },
  ok: { icon: CircleCheck, ink: 'text-ok-ink', fill: 'bg-ok-fill' },
} as const
const fills = { ok: 'bg-ok-fill', warn: 'bg-warn-fill', danger: 'bg-danger-fill' }
</script>

<template>
  <PageHeader :title="t('certs.list.title')" :subtitle="t('certs.list.intro')">
    <template #phone>
      <RouterLink
        :to="{ path: '/certificates/new', query: { source: 'upload' } }"
        :aria-label="t('certs.action.import')"
        class="grid size-11 place-items-center rounded-md text-ink hover:bg-surface-sunken"
      >
        <FileDown class="size-4" aria-hidden="true" />
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
      <JecButton :to="{ path: '/certificates/new', query: { source: 'upload' } }"><FileDown class="size-4" aria-hidden="true" />{{ t('certs.action.import') }}</JecButton>
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
      <div class="flex flex-wrap items-center gap-2">
        <SearchBox v-model="query" label="certs.search.label" class="w-full sm:max-w-80 sm:flex-[1_1_240px]" />
        <div role="group" :aria-label="t('certs.filter.label')" class="flex flex-wrap gap-2 sm:gap-1.5">
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
        <label v-if="sourceChoices.length + (hasUploads ? 1 : 0) > 1" class="flex items-center gap-2 text-small text-ink-muted sm:ml-auto">
          {{ t('certs.filter.source') }}
          <select
            v-model="fromSource"
            class="h-9 max-w-56 rounded-md border border-line-strong bg-surface-raised px-2 text-small text-ink sm:h-[30px]"
          >
            <option value="">{{ t('certs.filter.all_sources') }}</option>
            <option v-for="s in sourceChoices" :key="s.value" :value="s.value" translate="no">{{ s.name }}</option>
            <option v-if="hasUploads" value="upload">{{ t('certs.source.upload') }}</option>
          </select>
        </label>
      </div>

      <p v-if="!rows.length" class="m-0 rounded-lg border border-line bg-surface-raised p-4 text-ink-muted">
        {{ t('certs.list.no_match', { query }) }}
      </p>

      <!-- From 640px: the table. -->
      <div v-if="rows.length" class="hidden overflow-x-auto sm:block">
        <table class="w-full border-separate border-spacing-0 overflow-hidden rounded-lg border border-line bg-surface-raised">
          <thead>
            <tr>
              <th
                v-for="col in ['certs.column.domain', 'certs.column.state', 'certs.column.left', 'certs.column.source', 'certs.column.targets', 'certs.column.next'] as const"
                :key="col"
                scope="col"
                class="border-b border-line bg-surface-sunken px-3 py-2 text-left text-small font-semibold whitespace-nowrap text-ink-muted"
              >
                {{ t(col) }}
              </th>
              <th scope="col" class="relative w-11 border-b border-line bg-surface-sunken px-1 py-2">
                <span class="sr-only">{{ t('certs.column.actions') }}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.cert.id" class="hover:bg-surface-sunken [&:last-child>td]:border-b-0">
              <td class="border-b border-line px-3 py-1.5 whitespace-nowrap">
                <RouterLink :to="`/certificates/${r.cert.id}`" class="font-semibold text-ink no-underline hover:text-ink" translate="no">
                  {{ r.domain }}<small v-if="r.extra" class="ml-1 text-small font-normal text-ink-muted">{{ r.extra }}</small>
                </RouterLink>
              </td>
              <td class="border-b border-line px-3 py-1.5"><StatusBadge :state="r.cert.state" /></td>
              <td class="border-b border-line px-3 py-1.5">
                <LifeBar v-if="r.life" :life="r.life" inline />
                <span v-else class="text-small whitespace-nowrap text-ink-muted">{{ t('certs.life.pending') }}</span>
              </td>
              <td
                class="max-w-48 truncate border-b border-line px-3 py-1.5 whitespace-nowrap text-ink-muted"
                :translate="r.cert.source === 'provider' ? 'no' : undefined"
                :title="r.source"
              >
                {{ r.source }}
              </td>
              <td class="border-b border-line px-3 py-1.5">
                <span class="flex max-w-60 flex-wrap gap-1.5">
                  <span
                    v-for="name in r.targets"
                    :key="name"
                    class="inline-flex h-[22px] max-w-full items-center truncate rounded-sm border border-line px-2 text-caption whitespace-nowrap text-ink-muted"
                    translate="no"
                    :title="name"
                  >
                    {{ name }}
                  </span>
                </span>
              </td>
              <td class="max-w-56 truncate border-b border-line px-3 py-1.5 whitespace-nowrap text-ink-muted" :title="r.next">{{ r.next }}</td>
              <td class="border-b border-line px-1 py-1">
                <RowMenu :label="t('certs.menu.label', { domain: r.domain })" :items="menu(r.cert)" />
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="rows.length" class="m-0 hidden text-small text-ink-muted sm:block">
        {{ t('certs.list.summary', { shown: rows.length, total: all.length }) }}
      </p>

      <!-- Phones: the rows that need attention, then the tiles. -->
      <section v-if="attentionRows.length" aria-labelledby="group-attention" class="grid gap-2 sm:hidden">
        <h2 id="group-attention" class="m-0 flex items-baseline gap-2 px-1 text-small font-semibold text-ink-muted">
          {{ t('certs.attention.title') }}<data :value="attentionRows.length" class="tabular-nums">{{ attentionRows.length }}</data>
        </h2>
        <div role="list" class="overflow-hidden rounded-lg border border-line bg-surface-raised">
          <div v-for="r in attentionRows" :key="r.cert.id" role="listitem" class="border-b border-line last:border-b-0">
            <RouterLink
              :to="`/certificates/${r.cert.id}`"
              :aria-label="t('certs.cert.label_note', { label: r.label, note: r.note })"
              class="grid min-h-16 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 px-4 py-2.5 text-ink no-underline hover:bg-surface-sunken hover:text-ink"
            >
              <component :is="itemLooks[r.cert.state].icon" :class="['size-5', itemLooks[r.cert.state].ink]" aria-hidden="true" />
              <span class="grid min-w-0 gap-0.5">
                <span class="truncate text-heading font-semibold" translate="no" :title="intl.list(r.cert.domains)">
                  {{ r.domain }}<small v-if="r.extra" class="ml-1 text-caption font-normal text-ink-muted">{{ r.extra }}</small>
                </span>
                <span class="truncate text-small text-ink-muted" :title="t('certs.cert.label_note', { label: t(stateText[r.cert.state]), note: r.note })">
                  <span :class="['mr-2 font-semibold', itemLooks[r.cert.state].ink]">{{ t(stateText[r.cert.state]) }}</span>{{ r.note }}
                </span>
              </span>
              <span v-if="r.life" class="grid justify-items-end gap-1.5">
                <span :class="['text-body font-semibold whitespace-nowrap tabular-nums', itemLooks[r.cert.state].ink]">{{ r.lifeWords }}</span>
                <span class="h-1 w-14 bg-bar-track p-px shadow-[0_0_0_1px_var(--bar-edge)]">
                  <span :class="['block h-full', fills[r.life.level]]" :style="{ width: `${barPercent(r.life)}%` }" />
                </span>
              </span>
            </RouterLink>
          </div>
        </div>
      </section>

      <section v-if="healthyRows.length" aria-labelledby="group-ok" class="grid gap-2 sm:hidden">
        <h2 id="group-ok" class="m-0 flex items-baseline gap-2 px-1 text-small font-semibold text-ink-muted">
          {{ t('certs.state.ok') }}<data :value="healthyRows.length" class="tabular-nums">{{ healthyRows.length }}</data>
        </h2>
        <div role="list" class="grid grid-cols-2 gap-2">
          <div v-for="r in healthyRows" :key="r.cert.id" role="listitem" class="grid min-w-0">
            <RouterLink
              :to="`/certificates/${r.cert.id}`"
              :aria-label="r.label"
              class="grid min-w-0 gap-1.5 rounded-lg border border-line bg-surface-raised px-3 py-2.5 text-ink no-underline hover:bg-surface-sunken hover:text-ink"
            >
              <span class="truncate text-body font-semibold" translate="no" :title="intl.list(r.cert.domains)">
                {{ r.domain }}<small v-if="r.extra" class="ml-1 text-caption font-normal text-ink-muted">{{ r.extra }}</small>
              </span>
              <span v-if="r.life" class="flex items-center gap-2">
                <span class="h-1 min-w-0 flex-1 bg-bar-track p-px shadow-[0_0_0_1px_var(--bar-edge)]">
                  <span :class="['block h-full', fills[r.life.level]]" :style="{ width: `${barPercent(r.life)}%` }" />
                </span>
                <span class="flex-none text-small whitespace-nowrap text-ink-muted tabular-nums">{{ r.lifeWords }}</span>
              </span>
            </RouterLink>
          </div>
        </div>
      </section>
    </template>
  </div>

  <CertActionDialog v-model:ask="asking" />

  <JecDialog v-model:open="deleteOpen" :title="t('certs.delete.title', { domain: deleting?.domains[0] ?? '' })" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('certs.delete.body') }}</p>
      <JecAlert v-if="removeError.error.value" :code="removeError.error.value.code" :params="removeError.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="deleting = undefined">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="danger" :busy="remove.isPending.value" @click="confirmDelete">
        <Trash2 class="size-4" aria-hidden="true" />{{ t('certs.action.delete') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
