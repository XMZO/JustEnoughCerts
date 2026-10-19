<script setup lang="ts">
import { CalendarPlus, RefreshCw } from '@lucide/vue'
import { computed, ref } from 'vue'

import { feedURL, useFeed, useRotateFeed, useSwitchFeed } from '../api/calendar'
import { useApiError } from '../api/errors'
import { useT } from '../i18n'
import CopyButton from './CopyButton.vue'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecSwitch from './JecSwitch.vue'

// The calendar feed (module calendar): the certificates' ends and renewals
// at a secret URL that calendar apps subscribe to. Hidden while the module
// is off, as its API then is.
const { t, locale } = useT()
const feed = useFeed()
const toggle = useSwitchFeed()
const rotate = useRotateFeed()
const error = useApiError(computed(() => toggle.error.value ?? rotate.error.value))
const url = computed(() => (feed.data.value?.path ? feedURL(feed.data.value.path, locale.value) : ''))
const subscribe = computed(() => url.value.replace(/^https?:/, 'webcal:'))
const on = computed({
  get: () => !!feed.data.value?.enabled,
  set: (v: boolean) => toggle.mutate(v),
})
// Moving the feed breaks every subscription: the button asks once more.
const confirming = ref(false)
function move() {
  if (!confirming.value) {
    confirming.value = true
    return
  }
  confirming.value = false
  rotate.mutate()
}
</script>

<template>
  <section v-if="feed.isSuccess.value" aria-labelledby="calendar-feed" class="flex flex-col gap-2 rounded-lg border border-line bg-surface-raised p-4">
    <div class="flex items-start gap-3">
      <div class="min-w-0 flex-1">
        <h2 id="calendar-feed" class="m-0 text-heading font-semibold">{{ t('calendar.page.title') }}</h2>
        <p class="m-0 text-small text-ink-muted">{{ t('calendar.page.intro') }}</p>
      </div>
      <JecSwitch v-model="on" :label="t('calendar.page.title')" :disabled="toggle.isPending.value" class="mt-1" />
    </div>
    <JecAlert v-if="error.alert.value" :code="error.alert.value.code" :params="error.alert.value.params" />
    <template v-if="url">
      <div class="flex min-w-0 items-center gap-1 rounded-md border border-line bg-surface-sunken px-2 py-1">
        <code class="min-w-0 flex-1 truncate" translate="no" :title="url">{{ url }}</code>
        <CopyButton :value="url" label="calendar.page.copy" />
      </div>
      <p class="m-0 text-caption text-ink-muted">{{ t('calendar.page.secret') }}</p>
      <div class="flex flex-wrap gap-2">
        <!-- webcal: opens the calendar app of the device, which subscribes. -->
        <a
          :href="subscribe"
          class="jec-bevel inline-flex min-h-[30px] items-center gap-1.5 rounded-md border border-line-strong bg-surface-raised px-3 py-1 text-small font-semibold text-ink no-underline hover:bg-surface-sunken hover:text-ink max-sm:min-h-11"
        >
          <CalendarPlus class="size-4" aria-hidden="true" />{{ t('calendar.page.subscribe') }}
        </a>
        <JecButton size="sm" variant="ghost" :busy="rotate.isPending.value" @click="move">
          <RefreshCw class="size-4" aria-hidden="true" />{{ t(confirming ? 'calendar.page.rotate_confirm' : 'calendar.page.rotate') }}
        </JecButton>
      </div>
    </template>
  </section>
</template>
