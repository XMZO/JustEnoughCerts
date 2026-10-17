<script setup lang="ts">
import { Download } from '@lucide/vue'
import { nextTick, ref, watch } from 'vue'

import { saveFile, type LogLine } from '../api/certs'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecButton from './JecButton.vue'

// The log of an issuance or a deployment (docs/design/system/components/
// LogView.md): a time and a sentence a line, colored by level, following
// new lines unless someone scrolled up to read; the raw details on
// request, and the whole log as a download.
const props = defineProps<{
  lines: LogLine[]
  /** Shows the cursor of a log still growing. */
  running?: boolean
  /** The name of the downloaded file. */
  filename: string
}>()

const { t } = useT()
const intl = useIntl()

const message = (l: LogLine) => t(l.key as MessageKey, (l.params ?? {}) as Params)
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

/** The full log as text: every line with its time, attempt, level and raw detail. */
function download() {
  const text = props.lines
    .map((l) => {
      const head = `${l.at}  #${l.attempt}  ${l.level.toUpperCase().padEnd(5)}  ${message(l)}`
      return l.detail ? `${head}\n    ${l.detail.replaceAll('\n', '\n    ')}` : head
    })
    .join('\n')
  saveFile(new Blob([`${text}\n`], { type: 'text/plain' }), props.filename)
}
</script>

<template>
  <section aria-labelledby="log-title" class="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <h2 id="log-title" class="m-0 text-section-title font-semibold">{{ t('certs.issuance.log') }}</h2>
      <div class="flex flex-wrap items-center gap-2">
        <label class="flex min-h-9 items-center gap-1.5 text-small">
          <input v-model="showDetail" type="checkbox" class="size-4 accent-brand" />{{ t('certs.issuance.show_detail') }}
        </label>
        <JecButton size="sm" :disabled="!lines.length" @click="download">
          <Download class="size-4" aria-hidden="true" />{{ t('certs.issuance.download_log') }}
        </JecButton>
      </div>
    </div>
    <div
      ref="box"
      role="log"
      aria-live="polite"
      :aria-label="t('certs.issuance.log')"
      class="max-h-[60dvh] min-h-24 overflow-y-auto rounded-md bg-surface-sunken p-3 text-small"
      @scroll="onScroll"
    >
      <p v-if="!lines.length" class="m-0 text-ink-muted">{{ t('certs.issuance.no_log') }}</p>
      <div v-for="l in lines" :key="l.seq" class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 py-0.5">
        <time :datetime="l.at" class="text-ink-muted tabular-nums">{{ intl.time(l.at) }}</time>
        <span :class="['[overflow-wrap:anywhere]', levelInk[l.level], l.level === 'info' ? '' : 'font-semibold']">{{ message(l) }}</span>
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
