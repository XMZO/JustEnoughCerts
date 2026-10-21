<script setup lang="ts">
import { CircleCheck, CircleDashed, LoaderCircle, TriangleAlert } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import type { Health } from '../api/system'
import { useWindowHeight } from '../fit'
import { useT } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecPager from './JecPager.vue'

type Job = Health['jobs'][number]

// The background jobs and how each went last, those that failed first, a
// line each and paged: a page holds as many as fit the window, so that the
// dialog shows whole.
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ jobs: Job[]; moduleName: (j: Job) => string }>()

const { t } = useT()
const intl = useIntl()

const shown = computed(() => [...props.jobs].sort((a, b) => Number(!!b.error) - Number(!!a.error)))
// The dialog's head, words and foot take about 260px of the window; a
// failed job takes two lines.
const height = useWindowHeight()
const size = computed(() => Math.max(5, Math.floor((height.value - 300) / 34)))
const pages = computed(() => Math.max(1, Math.ceil(shown.value.length / size.value)))
const page = ref(1)
watch(open, (o) => {
  if (o) page.value = 1
})
watch(pages, (n) => {
  if (page.value > n) page.value = n
})
const pageJobs = computed(() => shown.value.slice((page.value - 1) * size.value, page.value * size.value))
</script>

<template>
  <JecDialog v-model:open="open" :title="t('kernel.health.jobs')" fit>
    <p class="m-0 mb-2 w-0 min-w-full text-small text-ink-muted">{{ t('kernel.health.help') }}</p>
    <ul class="m-0 list-none p-0 text-small">
      <li
        v-for="j in pageJobs"
        :key="j.name"
        class="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-2.5 border-b border-line py-1.5 last:border-b-0"
      >
        <TriangleAlert v-if="j.error" class="size-4 flex-none text-warn-ink" :aria-label="t('kernel.health.state_failed')" />
        <LoaderCircle v-else-if="j.running" class="size-4 flex-none animate-spin text-ink-muted" :aria-label="t('kernel.health.state_running')" />
        <CircleDashed v-else-if="!j.startedAt" class="size-4 flex-none text-ink-muted" :aria-label="t('kernel.health.state_never')" />
        <CircleCheck v-else class="size-4 flex-none text-ok-ink" :aria-label="t('kernel.health.state_ok')" />
        <span class="min-w-0 [overflow-wrap:anywhere]">
          <span class="font-semibold">{{ moduleName(j) }}</span>
          <code class="ml-2 text-caption text-ink-muted" translate="no">{{ j.name }}</code>
        </span>
        <span class="text-ink-muted whitespace-nowrap">
          <template v-if="j.startedAt">
            {{ t('kernel.health.last_run') }} <time :datetime="j.startedAt">{{ intl.dateTime(j.startedAt) }}</time>
          </template>
          <template v-else>{{ t('kernel.health.state_never') }}</template>
        </span>
        <span v-if="j.error" class="col-start-2 col-end-4 line-clamp-2 w-0 min-w-full text-warn-ink [overflow-wrap:anywhere]" translate="no" :title="j.error">
          {{ j.error }}
        </span>
      </li>
    </ul>
    <template #footer>
      <JecPager v-model="page" :pages="pages" class="mr-auto" />
      <JecButton @click="open = false">{{ t('kernel.action.close') }}</JecButton>
    </template>
  </JecDialog>
</template>
