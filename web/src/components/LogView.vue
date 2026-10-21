<script setup lang="ts">
import { Download } from '@lucide/vue'
import { computed, nextTick, ref, watch } from 'vue'

import { saveFile, type LogLine } from '../api/certs'
import { useManifest } from '../api/manifest'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'
import CopyButton from './CopyButton.vue'
import JecButton from './JecButton.vue'

// The log of an issuance or a deployment (docs/design/system/components/
// LogView.md): a time and a sentence a line, colored by level, following
// new lines unless someone scrolled up to read; the raw details on
// request, when a line has any, and the whole log as a download or to
// copy, as into a report, with the version of JEC first.
const props = defineProps<{
  lines: LogLine[]
  /** Shows the cursor of a log still growing. */
  running?: boolean
  /** The name of the downloaded file. */
  filename: string
}>()

const { t } = useT()
const intl = useIntl()
const manifest = useManifest()

const message = (l: LogLine) => t(l.key as MessageKey, (l.params ?? {}) as Params)
// A record to add to the DNS, as a validation by hand needs: its name and
// value, each to copy.
const record = (l: LogLine) => {
  const p = (l.params ?? {}) as Record<string, unknown>
  return l.key.endsWith('.log.record') && typeof p.name === 'string' && typeof p.value === 'string' ? { name: p.name, value: p.value } : undefined
}
const levelInk = { info: 'text-ink', ok: 'text-ok-ink', warn: 'text-warn-ink', error: 'text-danger-ink' } as const

const box = ref<HTMLElement>()
const follow = ref(true)
function onScroll() {
  const el = box.value
  if (el) follow.value = el.scrollHeight - el.scrollTop - el.clientHeight < 24
}
watch(
  () => props.lines.length,
  async () => {
    await nextTick()
    if (follow.value && box.value) box.value.scrollTop = box.value.scrollHeight
  },
)
const showDetail = ref(false)
const hasDetail = computed(() => props.lines.some((l) => l.detail))

/** The full log as text: every line with its time, attempt, level and raw detail. */
const text = computed(() =>
  props.lines
    .map((l) => {
      const head = `${l.at}  #${l.attempt}  ${l.level.toUpperCase().padEnd(5)}  ${message(l)}`
      return l.detail ? `${head}\n    ${l.detail.replaceAll('\n', '\n    ')}` : head
    })
    .join('\n'),
)
// Copied, it says which log of which JEC it is.
const copyText = computed(() => {
  const m = manifest.data.value
  return `${props.filename}${m ? `  (JEC ${m.version}, ${m.platform})` : ''}\n${text.value}\n`
})
function download() {
  saveFile(new Blob([`${text.value}\n`], { type: 'text/plain' }), props.filename)
}
</script>

<template>
  <section aria-labelledby="log-title" class="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 id="log-title" class="m-0 text-section-title font-semibold">{{ t('certs.issuance.log') }}</h2>
      <div class="flex flex-wrap items-center gap-2">
        <label v-if="hasDetail" class="flex min-h-9 items-center gap-1.5 text-small">
          <input v-model="showDetail" type="checkbox" class="size-4 accent-brand" />{{ t('certs.issuance.show_detail') }}
        </label>
        <JecButton size="sm" :disabled="!lines.length" @click="download">
          <Download class="size-4" aria-hidden="true" />{{ t('certs.issuance.download_log') }}
        </JecButton>
        <CopyButton v-if="lines.length" :value="copyText" label="certs.issuance.copy_log" />
      </div>
    </div>
    <div
      ref="box"
      role="log"
      aria-live="polite"
      :aria-label="t('certs.issuance.log')"
      tabindex="0"
      class="max-h-[60dvh] min-h-24 overflow-y-auto rounded-md bg-surface-sunken p-3 text-small"
      @scroll="onScroll"
    >
      <p v-if="!lines.length" class="m-0 text-ink-muted">{{ t('certs.issuance.no_log') }}</p>
      <div v-for="l in lines" :key="l.seq" class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 py-0.5">
        <time :datetime="l.at" class="text-ink-muted tabular-nums">{{ intl.time(l.at) }}</time>
        <span :class="['[overflow-wrap:anywhere]', levelInk[l.level], l.level === 'info' ? '' : 'font-semibold']">{{ message(l) }}</span>
        <span v-if="record(l)" class="col-start-2 flex flex-wrap items-center gap-x-3 gap-y-1">
          <span class="inline-flex min-w-0 items-center gap-1">
            <code class="[overflow-wrap:anywhere]" translate="no">{{ record(l)!.name }}</code>
            <CopyButton :value="record(l)!.name" label="certs.issuance.copy_name" />
          </span>
          <span class="inline-flex min-w-0 items-center gap-1">
            <code class="[overflow-wrap:anywhere]" translate="no">{{ record(l)!.value }}</code>
            <CopyButton :value="record(l)!.value" label="certs.issuance.copy_value" />
          </span>
        </span>
        <pre
          v-if="showDetail && l.detail"
          translate="no"
          class="col-start-2 m-0 font-mono text-caption whitespace-pre-wrap text-ink-muted [overflow-wrap:anywhere]"
        >{{ l.detail }}</pre>
      </div>
      <div v-if="running" class="py-0.5" aria-hidden="true"><span class="inline-block h-4 w-2 animate-pulse bg-ink-muted align-middle" /></div>
    </div>
  </section>
</template>
