<script setup lang="ts">
import { CircleCheck, FlaskConical, Pencil, PlugZap, Plus, Send, Trash2, TriangleAlert } from '@lucide/vue'
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { useCertificates, useRuns } from '../api/certs'
import { useCredentials } from '../api/credentials'
import { useManifest } from '../api/manifest'
import { ApiError, networkError } from '../api/client'
import { useApiError } from '../api/errors'
import { useSends, useSendTest } from '../api/notify'
import { useDeleteProvider, useProviders, useProviderTest, useProviderTypes, useProviderTypesOf, type Provider } from '../api/providers'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import PageHeader from '../components/PageHeader.vue'
import CalendarFeed from '../components/CalendarFeed.vue'
import ProviderDialog from '../components/ProviderDialog.vue'
import TryDnsDialog from '../components/TryDnsDialog.vue'
import TypeTags from '../components/TypeTags.vue'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'
import { kindPages, summary, typeName, type PageKind } from '../providers/fields'
import { fromMore } from '../shell'

// The configured providers of one kind (docs/design/boards/Providers):
// certificate sources, DNS providers for the domain validation, deploy
// targets or notifiers. Each card says what it is, what it is set to and
// how many certificates use it, and tests it when its type can be; a
// notifier's card says instead what it tells about and how its last note
// went, and sends a test note.
const props = defineProps<{ kind: PageKind }>()

const { t } = useT()
const intl = useIntl()
const page = computed(() => kindPages[props.kind])
const list = useProviders(() => props.kind)
const types = useProviderTypes(() => props.kind)
const certificates = useCertificates()
const credentials = useCredentials()
// The deployments of the last month, for the cards of targets.
const runs = useRuns(30, () => props.kind === 'target')
const loadError = useApiError(computed(() => list.error.value))

// The full types of the instances, for the settings their cards show.
const fullType = useProviderTypesOf(() => [...new Set((list.data.value ?? []).map((p) => p.type))])

// Notifiers: what they tell about, by the switches every notifier type
// has, and a test on demand, while the notify module, which sends the
// notes, is on; when it is off, the page says so.
const notifiers = computed(() => props.kind === 'notifier')
const manifest = useManifest()
const dispatchOff = computed(() => notifiers.value && manifest.data.value?.modules.some((m) => m.id === 'notify' && !m.enabled) === true)
const sends = useSends(computed(() => notifiers.value && !dispatchOff.value))
// The events a notifier is told about, as the server decides: those it
// was set up before have their defaults.
const noteEvents = [
  { key: 'on_issue_failed', label: 'notify.event.issue_failed', on: true },
  { key: 'on_deploy_failed', label: 'notify.event.deploy_failed', on: true },
  { key: 'on_expiring', label: 'notify.event.expiring', on: true },
  { key: 'on_reminder', label: 'notify.event.reminder', on: true },
  { key: 'on_served_old', label: 'notify.event.served_old', on: true },
  { key: 'on_chain_incomplete', label: 'notify.event.chain_incomplete', on: true },
  { key: 'on_revoked', label: 'notify.event.revoked', on: true },
  { key: 'on_site_expiring', label: 'notify.event.site_expiring', on: true },
  { key: 'on_domain_expiring', label: 'notify.event.domain_expiring', on: true },
  { key: 'on_spotted', label: 'notify.event.spotted', on: true },
  { key: 'on_provider_failing', label: 'notify.event.provider_failing', on: true },
  { key: 'on_locked', label: 'notify.event.locked', on: true },
  { key: 'on_release', label: 'notify.event.release', on: true },
  { key: 'on_issued', label: 'notify.event.issued', on: false },
  { key: 'on_deployed', label: 'notify.event.deployed', on: false },
  { key: 'on_digest', label: 'notify.event.digest', on: false },
  { key: 'on_sign_in', label: 'notify.event.sign_in', on: false },
] as const
const quietText: Record<string, MessageKey> = {
  '22-7': 'kernel.notify.quiet.h22_to7',
  '23-8': 'kernel.notify.quiet.h23_to8',
  '0-8': 'kernel.notify.quiet.h0_to8',
}
function events(p: Provider): string {
  const on = noteEvents.filter((e) => (p.config[e.key] ?? String(e.on)) === 'true').map((e) => t(e.label))
  const text = on.length ? t('notify.card.events', { events: intl.list(on) }) : t('notify.card.no_events')
  const quiet = quietText[p.config.quiet_hours ?? '']
  return quiet ? `${text} ${t('notify.card.quiet', { hours: t(quiet) })}` : text
}
const errorText = (e?: { key: string; params?: Record<string, unknown> }) => (e ? t(e.key as MessageKey, (e.params ?? {}) as Params) : '')

// How a provider fared last (docs/design/boards/Providers): what its last
// test said, or, for a target, how its last deployment went, whichever
// came later.
interface CardStatus {
  ok: boolean
  text: string
  at: string
}
function statusOf(p: Provider): CardStatus | undefined {
  const found: CardStatus[] = []
  if (p.lastTest) {
    const said = errorText(p.lastTest.message)
    found.push({ ok: p.lastTest.ok, at: p.lastTest.at, text: p.lastTest.ok ? said : t('kernel.providers.status.test_failed', { error: said }) })
  }
  if (props.kind === 'target') {
    // A deployment that failed and is tried again later waits, with why.
    const ended = (r: { state: string; error?: unknown }) => r.state === 'done' || r.state === 'failed' || (r.state === 'pending' && !!r.error)
    const d = runs.data.value?.runs?.find((r) => r.kind === 'deployment' && r.target === p.id && ended(r))
    const at = d?.finishedAt ?? d?.startedAt
    if (d && at) {
      found.push({
        ok: d.state === 'done',
        at,
        text: d.state === 'done' ? t('kernel.providers.status.deployed') : t('kernel.providers.status.deploy_failed', { error: errorText(d.error) }),
      })
    }
  }
  return found.sort((a, b) => Date.parse(b.at) - Date.parse(a.at))[0]
}

// Tests: a note through a notifier, or a check of the settings of any
// other provider whose type has one. Each card keeps what its last test
// said: the words of success, or the error.
const sendTest = useSendTest()
const providerTest = useProviderTest()
const testing = ref<number>()
const tested = ref<Record<number, ApiError | string>>({})
const testError = (id: number) => {
  const r = tested.value[id]
  return r instanceof ApiError ? r : undefined
}
const testPassed = (id: number) => {
  const r = tested.value[id]
  return typeof r === 'string' ? r : undefined
}
async function test(id: number) {
  testing.value = id
  try {
    let said: string
    if (notifiers.value) {
      await sendTest.mutateAsync(id)
      said = t('notify.test.sent')
    } else {
      const { message } = await providerTest.mutateAsync(id)
      said = t(message.key as MessageKey, (message.params ?? {}) as Params)
    }
    tested.value = { ...tested.value, [id]: said }
  } catch (err) {
    tested.value = { ...tested.value, [id]: err instanceof ApiError ? err : networkError }
  } finally {
    testing.value = undefined
  }
}

const cards = computed(() =>
  (list.data.value ?? []).map((p) => {
    const summaryType = types.data.value?.find((s) => s.id === p.type)
    const name = typeName(summaryType)
    const uses = (certificates.data.value ?? []).filter(
      (c) => c.sourceProvider === p.id || c.solverProvider === p.id || c.targets.includes(p.id),
    ).length
    return {
      provider: p,
      type: summaryType,
      typeBrand: name.brand,
      typeLabel: name.key ? t(name.key) : (name.brand ?? (summaryType ? '' : t('kernel.providers.type_gone'))),
      shown: summary(fullType(p.type), p),
      secrets: p.secretKeys.length,
      credential: p.credentialId ? (credentials.data.value?.find((c) => c.id === p.credentialId)?.name ?? '') : undefined,
      status: notifiers.value ? undefined : statusOf(p),
      uses,
      events: notifiers.value ? events(p) : '',
      send: sends.data.value?.find((s) => s.notifier === p.id),
      testable: notifiers.value ? !dispatchOff.value : !!summaryType?.testable,
      // A solver that adds records or puts files itself can be tried on a
      // domain.
      triable: props.kind === 'solver' && !summaryType?.manual && !!summaryType,
      http: summaryType?.challenge === 'http',
    }
  }),
)

const adding = ref(false)
// A solver of DNS tried on a domain: one of a certificate that uses it,
// to start with.
const trying = ref<Provider>()
const tryOpen = computed({
  get: () => trying.value !== undefined,
  set: (open) => {
    if (!open) trying.value = undefined
  },
})
const tryDomain = (id: number) => {
  const d = certificates.data.value?.find((c) => c.solverProvider === id)?.domains[0] ?? ''
  return d.replace(/^\*\./, '')
}

const editing = ref<Provider>()
const editOpen = computed({
  get: () => editing.value !== undefined,
  set: (open) => {
    if (!open) editing.value = undefined
  },
})
// A link to one of them, ?edit=<id> as the quick jump makes, opens it.
const route = useRoute()
const router = useRouter()
watch(
  [() => route.query.edit, () => list.data.value],
  ([edit, providers]) => {
    if (typeof edit !== 'string' || !providers) return
    const p = providers.find((x) => String(x.id) === edit)
    if (p) editing.value = p
    void router.replace({ query: { ...route.query, edit: undefined } })
  },
  { immediate: true },
)

const deleting = ref<Provider>()
const deleteOpen = computed({
  get: () => deleting.value !== undefined,
  set: (open) => {
    if (!open) deleting.value = undefined
  },
})
const remove = useDeleteProvider()
const removeError = useApiError(remove.error)
function startDelete(p: Provider) {
  remove.reset()
  deleting.value = p
}
async function confirmDelete() {
  if (!deleting.value) return
  try {
    await remove.mutateAsync(deleting.value.id)
  } catch {
    return // shown in the dialog
  }
  deleting.value = undefined
}
</script>

<template>
  <PageHeader :title="t(page.title)" :subtitle="t(page.intro)" :phone-back="fromMore">
    <template #phone>
      <button
        type="button"
        :aria-label="t(page.add)"
        class="grid size-11 cursor-pointer place-items-center rounded-md border-0 bg-brand text-on-brand hover:bg-brand-hover"
        @click="adding = true"
      >
        <Plus class="size-4" aria-hidden="true" />
      </button>
    </template>
    <template #actions>
      <JecButton variant="primary" @click="adding = true"><Plus class="size-4" aria-hidden="true" />{{ t(page.add) }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t(page.intro) }}</p>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />
    <p v-if="dispatchOff" role="status" class="m-0 rounded-md bg-warn-soft p-3 text-small text-warn-ink">
      {{ t('kernel.providers.notifier.dispatch_off') }}
      <RouterLink to="/modules" class="font-semibold text-warn-ink">{{ t('kernel.modules.title') }}</RouterLink>
    </p>

    <section v-if="list.isSuccess.value && !cards.length" class="flex flex-col items-start gap-3 rounded-lg border border-line bg-surface-raised p-4">
      <p class="m-0 text-ink-muted">{{ t(page.empty) }}</p>
      <JecButton variant="primary" @click="adding = true"><Plus class="size-4" aria-hidden="true" />{{ t(page.add) }}</JecButton>
    </section>

    <div v-else-if="cards.length" class="grid grid-cols-[repeat(auto-fill,minmax(min(100%,300px),1fr))] gap-3">
      <article v-for="c in cards" :key="c.provider.id" class="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
        <div class="flex min-w-0 flex-col gap-0.5">
          <h2 class="m-0 truncate text-heading font-semibold" translate="no" :title="c.provider.name">{{ c.provider.name }}</h2>
          <span class="flex min-w-0 items-center gap-1.5">
            <span class="truncate text-small text-ink-muted" :translate="c.typeBrand ? 'no' : undefined" :title="c.typeLabel">{{ c.typeLabel }}</span>
            <TypeTags :type="c.type" />
          </span>
        </div>
        <dl v-if="c.shown.length" class="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-small">
          <template v-for="s in c.shown" :key="s.key">
            <!-- A long label wraps rather than push the card wider. -->
            <dt class="max-w-40 text-ink-muted [overflow-wrap:anywhere]">
              <template v-if="s.label">{{ t(s.label) }}</template>
              <code v-else class="font-mono">{{ s.key }}</code>
            </dt>
            <dd v-if="s.option" class="m-0 truncate" :title="t(s.option)">{{ t(s.option) }}</dd>
            <dd v-else class="m-0 truncate" translate="no" :title="s.value">{{ s.value }}</dd>
          </template>
        </dl>
        <p
          v-if="c.credential !== undefined"
          class="m-0 truncate text-small text-ink-muted"
          :title="t('kernel.providers.credential.card', { name: c.credential })"
        >
          {{ t('kernel.providers.credential.card', { name: c.credential }) }}
        </p>
        <p v-if="c.credential === undefined || c.secrets" class="m-0 text-small text-ink-muted">
          {{ c.secrets ? t('kernel.providers.secrets_saved', { count: c.secrets }) : t('kernel.providers.no_secrets') }}
        </p>
        <template v-if="notifiers">
          <p class="m-0 text-small text-ink-muted">{{ c.events }}</p>
          <!-- Until a test says how it went, the last note does. -->
          <template v-if="!tested[c.provider.id]">
            <p v-if="!c.send" class="m-0 text-small text-ink-muted">{{ t('notify.card.never') }}</p>
            <p v-else-if="c.send.state === 'sent'" class="m-0 text-small text-ok-ink">
              {{ t('notify.card.sent') }} <time :datetime="c.send.at">{{ intl.dateTime(c.send.at) }}</time>
            </p>
            <p v-else-if="c.send.state === 'held'" class="m-0 text-small text-ink-muted">
              {{ t('notify.card.held') }} <time v-if="c.send.sendAt" :datetime="c.send.sendAt">{{ intl.dateTime(c.send.sendAt) }}</time>
            </p>
            <p v-else class="m-0 text-small text-danger-ink [overflow-wrap:anywhere]">{{ t('notify.card.failed', { error: errorText(c.send.error) }) }}</p>
          </template>
        </template>
        <template v-else>
          <p
            v-if="c.status && !tested[c.provider.id]"
            :class="['m-0 flex items-start gap-1.5 text-small [overflow-wrap:anywhere]', c.status.ok ? 'text-ok-ink' : 'text-warn-ink']"
          >
            <component :is="c.status.ok ? CircleCheck : TriangleAlert" class="mt-0.5 size-4 flex-none" aria-hidden="true" />
            <span>
              {{ c.status.text }} <time :datetime="c.status.at" class="text-ink-muted">{{ intl.dateTime(c.status.at) }}</time>
            </span>
          </p>
          <p class="m-0 text-small text-ink-muted">{{ t('kernel.providers.uses', { count: c.uses }) }}</p>
        </template>
        <p v-if="testPassed(c.provider.id)" role="status" class="m-0 text-small text-ok-ink [overflow-wrap:anywhere]">{{ testPassed(c.provider.id) }}</p>
        <JecAlert v-else-if="testError(c.provider.id)" :code="testError(c.provider.id)!.code" :params="testError(c.provider.id)!.params" />
        <div class="mt-auto flex flex-wrap gap-2 pt-1">
          <JecButton v-if="c.testable" size="sm" :busy="testing === c.provider.id" @click="test(c.provider.id)">
            <component :is="notifiers ? Send : PlugZap" class="size-4" aria-hidden="true" />{{ t(notifiers ? 'notify.action.test' : 'kernel.providers.test') }}
          </JecButton>
          <JecButton v-if="c.triable" size="sm" @click="trying = c.provider">
            <FlaskConical class="size-4" aria-hidden="true" />{{ t('kernel.providers.try') }}
          </JecButton>
          <JecButton size="sm" @click="editing = c.provider"><Pencil class="size-4" aria-hidden="true" />{{ t('kernel.action.edit') }}</JecButton>
          <JecButton size="sm" variant="ghost" @click="startDelete(c.provider)"><Trash2 class="size-4" aria-hidden="true" />{{ t('kernel.action.delete') }}</JecButton>
        </div>
      </article>
      <button
        type="button"
        class="flex min-h-32 cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-line-strong bg-transparent p-4 font-semibold text-ink-muted hover:bg-surface-sunken hover:text-ink"
        @click="adding = true"
      >
        <Plus class="size-5" aria-hidden="true" />{{ t(page.add) }}
      </button>
    </div>

    <p v-if="types.data.value" class="m-0 text-small text-ink-muted">
      {{ t(page.foot, { count: types.data.value.length }) }}
      <RouterLink to="/modules" class="whitespace-nowrap underline underline-offset-2">{{ t('kernel.providers.manage_modules') }}</RouterLink>
    </p>

    <CalendarFeed v-if="notifiers" />
  </div>

  <ProviderDialog v-model:open="adding" :kind="kind" :title="t(page.add)" />
  <TryDnsDialog
    v-if="trying"
    v-model:open="tryOpen"
    :id="trying.id"
    :name="trying.name"
    :http="cards.find((c) => c.provider.id === trying?.id)?.http ?? false"
    :suggested="tryDomain(trying.id)"
  />
  <ProviderDialog v-if="editing" v-model:open="editOpen" :kind="kind" :instance="editing" :title="t('kernel.providers.edit', { name: editing.name })" />

  <JecDialog v-model:open="deleteOpen" :title="t('kernel.providers.delete.title', { name: deleting?.name ?? '' })" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('kernel.providers.delete.body') }}</p>
      <JecAlert v-if="removeError.error.value" :code="removeError.error.value.code" :params="removeError.error.value.params" />
    </div>
    <template #footer>
      <JecButton @click="deleteOpen = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="danger" :busy="remove.isPending.value" @click="confirmDelete">
        <Trash2 class="size-4" aria-hidden="true" />{{ t('kernel.action.delete') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
