<script setup lang="ts">
import { computed, ref } from 'vue'

import { useApiError } from '../api/errors'
import { useManifest, useSwitchModule, type ModuleInfo } from '../api/manifest'
import JecAlert from '../components/JecAlert.vue'
import JecSwitch from '../components/JecSwitch.vue'
import PageHeader from '../components/PageHeader.vue'
import SearchBox from '../components/SearchBox.vue'
import { useT, type MessageKey } from '../i18n'

// The modules (docs/design/boards/Modules and ModulesMobile): every source,
// solver, target, notifier and feature compiled in, by category, each with
// a switch that acts at once. Required modules stay on; experimental ones
// start off. On phones a whole tile is the switch's target.
const { t } = useT()
const manifest = useManifest()
const loadError = useApiError(computed(() => manifest.error.value))
const switcher = useSwitchModule()
const switchError = useApiError(computed(() => switcher.error.value))

const categories: { id: ModuleInfo['category']; title: MessageKey }[] = [
  { id: 'source', title: 'kernel.providers.source.title' },
  { id: 'solver', title: 'kernel.providers.solver.title' },
  { id: 'target', title: 'kernel.providers.target.title' },
  { id: 'notifier', title: 'kernel.providers.notifier.title' },
  { id: 'feature', title: 'kernel.modules.feature' },
]
const modules = computed(() => manifest.data.value?.modules ?? [])
const isOn = (m: ModuleInfo) => m.required || m.switched
const name = (m: ModuleInfo) => t(m.name as MessageKey)
const description = (m: ModuleInfo) => t(m.description as MessageKey)
// A module switched on that waits for the modules it requires.
function waiting(m: ModuleInfo): string {
  if (!m.switched || m.enabled) return ''
  const off = (m.requires ?? []).map((id) => modules.value.find((r) => r.id === id)).filter((r) => r && !r.enabled)
  return t('kernel.modules.waiting', { modules: off.map((r) => name(r!)).join(', ') })
}

type Filter = 'all' | 'on' | 'off'
const filter = ref<Filter>('all')
const query = ref('')
const matches = (m: ModuleInfo, f: Filter) => f === 'all' || (f === 'on') === isOn(m)
const filters = computed(() =>
  (
    [
      { id: 'all', label: 'kernel.modules.filter.all' },
      { id: 'on', label: 'kernel.modules.filter.on' },
      { id: 'off', label: 'kernel.modules.filter.off' },
    ] as const
  ).map((f) => ({ ...f, count: modules.value.filter((m) => matches(m, f.id)).length })),
)
const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  return modules.value
    .filter((m) => matches(m, filter.value))
    .filter((m) => !q || m.id.includes(q) || name(m).toLowerCase().includes(q) || description(m).toLowerCase().includes(q))
})
const groups = computed(() =>
  categories
    .map((c) => {
      const all = modules.value.filter((m) => m.category === c.id)
      return { ...c, mods: shown.value.filter((m) => m.category === c.id), on: all.filter(isOn).length, total: all.length }
    })
    .filter((g) => g.mods.length),
)
const onCount = computed(() => modules.value.filter(isOn).length)

const busy = ref<string>()
async function toggle(m: ModuleInfo, on: boolean) {
  if (m.required || busy.value) return
  busy.value = m.id
  try {
    await switcher.mutateAsync({ id: m.id, on })
  } catch {
    // shown above the modules
  } finally {
    busy.value = undefined
  }
}
// A tap on a tile, outside its switch, which handles its own.
function tap(e: MouseEvent, m: ModuleInfo) {
  if (!(e.target as HTMLElement).closest('button')) void toggle(m, !isOn(m))
}

// A tag goes under the name when the two do not fit, and is cut short, with
// its full text as a title, when it alone does not.
const tag = 'inline-block max-w-full truncate rounded-sm px-1.5 text-[11px] leading-[18px] font-semibold'
</script>

<template>
  <PageHeader :title="t('kernel.modules.title')" :subtitle="t('kernel.modules.intro')" />

  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-4 sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t('kernel.modules.intro') }}</p>
    <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />
    <JecAlert v-if="switchError.error.value" :code="switchError.error.value.code" :params="switchError.error.value.params" />

    <div class="flex flex-wrap items-center gap-2">
      <SearchBox v-model="query" label="kernel.modules.search" class="w-full sm:max-w-72 sm:flex-[1_1_220px]" />
      <div role="group" :aria-label="t('kernel.modules.filter.label')" class="flex flex-wrap gap-2 sm:gap-1.5">
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
    </div>

    <section
      v-for="g in groups"
      :key="g.id"
      :aria-labelledby="`modules-${g.id}`"
      class="flex flex-col gap-2.5 rounded-lg border border-line bg-surface-raised p-3 sm:p-4"
    >
      <div class="flex flex-wrap items-baseline justify-between gap-x-3">
        <h2 :id="`modules-${g.id}`" class="m-0 text-heading font-semibold sm:text-section-title">{{ t(g.title) }}</h2>
        <span class="text-small text-ink-muted">{{ t('kernel.modules.count', { on: g.on, total: g.total }) }}</span>
      </div>
      <ul class="m-0 grid list-none grid-cols-2 gap-1.5 p-0 sm:grid-cols-[repeat(auto-fill,minmax(240px,1fr))] sm:gap-2">
        <li
          v-for="m in g.mods"
          :key="m.id"
          :class="[
            'flex min-h-11 min-w-0 items-center gap-2 rounded-md border border-line bg-surface-raised px-2.5 py-2 sm:gap-3 sm:px-3 sm:py-2.5',
            m.required ? '' : 'max-sm:cursor-pointer',
          ]"
          @click="tap($event, m)"
        >
          <div class="min-w-0 flex-1">
            <div class="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5 text-small font-semibold sm:text-body">
              <span class="min-w-0 truncate" :title="name(m)">{{ name(m) }}</span>
              <span v-if="m.required" :class="[tag, 'bg-surface-sunken text-ink-muted max-sm:hidden']" :title="t('kernel.modules.builtin')">
                {{ t('kernel.modules.builtin') }}
              </span>
              <span v-if="m.experimental" :class="[tag, 'bg-warn-soft text-warn-ink']" :title="t('kernel.modules.experimental')">
                {{ t('kernel.modules.experimental') }}
              </span>
            </div>
            <p v-if="waiting(m)" class="m-0 text-caption text-warn-ink [overflow-wrap:anywhere]">{{ waiting(m) }}</p>
            <p v-else class="m-0 truncate text-caption text-ink-muted max-sm:hidden" :title="description(m)">{{ description(m) }}</p>
          </div>
          <JecSwitch
            :model-value="isOn(m)"
            :label="t('kernel.modules.switch', { name: name(m) })"
            :disabled="m.required || busy === m.id"
            @update:model-value="(on) => toggle(m, on)"
          />
        </li>
      </ul>
    </section>

    <p v-if="modules.length && !groups.length" class="m-0 text-ink-muted">{{ t('kernel.modules.no_match') }}</p>
    <p v-if="modules.length" class="m-0 text-small text-ink-muted">{{ t('kernel.modules.summary', { on: onCount, total: modules.length }) }}</p>
  </div>
</template>
