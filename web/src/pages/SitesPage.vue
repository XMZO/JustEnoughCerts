<script setup lang="ts">
import { CircleCheck, Clock, OctagonX, Plus, RefreshCw, ShieldAlert, Trash2, TriangleAlert, Unplug } from '@lucide/vue'
import { computed, ref } from 'vue'

import { useApiError } from '../api/errors'
import { useAddSite, useCheckSite, useDeleteSite, useSites, type Site } from '../api/watch'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import PageHeader from '../components/PageHeader.vue'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import { fromMore } from '../shell'

// The sites JEC watches but does not manage (module watch): what each
// serves, whether it is trusted and when it ends. On phones a list, from
// 640px a table.
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
const address = (s: Site) => (s.port === 443 ? s.host : s.host.includes(':') ? `[${s.host}]:${s.port}` : `${s.host}:${s.port}`)
const day = 86_400_000
// How long is left, or how long ago it ended.
const left = (s: Site) => {
  const days = Math.floor((Date.parse(s.notAfter ?? '') - Date.now()) / day)
  return days < 0 ? t('watch.page.days_ago', { days: -days }) : t('watch.page.days_left', { days })
}

// Adding: a host or a URL, and a name.
const adding = ref(false)
const host = ref('')
const name = ref('')
const add = useAddSite()
const addError = useApiError(computed(() => add.error.value))
function openAdd() {
  host.value = ''
  name.value = ''
  add.reset()
  adding.value = true
}
async function submitAdd() {
  try {
    await add.mutateAsync({ host: host.value, name: name.value || undefined })
  } catch {
    return // the dialog shows why
  }
  adding.value = false
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
</script>

<template>
  <PageHeader :title="t('watch.page.title')" :subtitle="t('watch.page.intro')" :phone-back="fromMore">
    <template #actions>
      <JecButton variant="primary" @click="openAdd"><Plus class="size-4" aria-hidden="true" />{{ t('watch.page.add') }}</JecButton>
    </template>
  </PageHeader>

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t('watch.page.intro') }}</p>
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
            <td class="border-b border-line px-3 py-2 [overflow-wrap:anywhere]">
              <span v-if="s.name" class="block font-semibold" translate="no">{{ s.name }}</span>
              <span :class="['block', s.name ? 'text-caption text-ink-muted' : 'font-semibold']" translate="no">{{ address(s) }}</span>
            </td>
            <td class="border-b border-line px-3 py-2">
              <span :class="['inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-semibold whitespace-nowrap', looks[s.state].badge]">
                <component :is="looks[s.state].icon" class="size-3.5" aria-hidden="true" />{{ t(looks[s.state].label) }}
              </span>
              <span v-if="s.error" class="mt-1 block max-w-64 truncate text-caption text-ink-muted" :title="s.error" translate="no">{{ s.error }}</span>
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
        <component :is="looks[s.state].icon" :class="['mt-0.5 size-5', looks[s.state].ink]" aria-hidden="true" />
        <span class="grid min-w-0 gap-0.5">
          <span class="truncate font-semibold" translate="no" :title="s.name || address(s)">{{ s.name || address(s) }}</span>
          <span v-if="s.name" class="truncate text-caption text-ink-muted" translate="no" :title="address(s)">{{ address(s) }}</span>
          <span class="flex flex-wrap gap-x-2 text-small">
            <span :class="['font-semibold', looks[s.state].ink]">{{ t(looks[s.state].label) }}</span>
            <template v-if="s.notAfter">
              <time :datetime="s.notAfter" class="text-ink-muted">{{ intl.date(s.notAfter) }}</time>
              <span class="text-ink-muted">{{ left(s) }}</span>
            </template>
          </span>
          <span v-if="s.error" class="truncate text-caption text-ink-muted" :title="s.error" translate="no">{{ s.error }}</span>
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

    <p class="m-0 text-small text-ink-muted">{{ t('watch.page.foot') }}</p>
  </div>

  <JecDialog v-model:open="adding" :title="t('watch.page.add')" small>
    <form id="site-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitAdd">
      <JecAlert v-if="addError.alert.value" :code="addError.alert.value.code" :params="addError.alert.value.params" />
      <JecField id="site-host" label="watch.field.host" hint="watch.field.host_help" :error="addError.fields.value.host">
        <JecInput id="site-host" v-model="host" :invalid="!!addError.fields.value.host" autocomplete="off" required />
      </JecField>
      <JecField id="site-name" label="watch.field.name" hint="watch.field.name_help">
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
</template>
