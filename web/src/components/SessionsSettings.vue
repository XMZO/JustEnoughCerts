<script setup lang="ts">
import { LogOut, MonitorSmartphone } from '@lucide/vue'
import { computed, ref } from 'vue'

import { useEndSession, useSessions, type Session } from '../api/account'
import { useApiError } from '../api/errors'
import { useT, type MessageKey } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'

// Where the account is signed in: a row of the account settings, and the
// dialog that lists the sessions, each with where it came from, to sign
// one out or all but this one.
defineProps<{ rowClass: string }>()

const { t } = useT()
const intl = useIntl()
const open = ref(false)
const sessions = useSessions(open)
const end = useEndSession()
const endError = useApiError(computed(() => end.error.value))
const ending = ref<string>()

const vias: Record<NonNullable<Session['via']>, MessageKey> = {
  password: 'kernel.sessions.via_password',
  sso: 'kernel.sessions.via_sso',
  setup: 'kernel.sessions.via_setup',
}

function show() {
  end.reset()
  open.value = true
}
async function signOut(id: string) {
  ending.value = id
  try {
    await end.mutateAsync(id)
  } catch {
    // the alert shows why
  } finally {
    ending.value = undefined
  }
}
</script>

<template>
  <div :class="[rowClass, 'flex-wrap']">
    <div class="min-w-0 flex-[1_1_8rem]">
      <span class="block font-semibold">{{ t('kernel.sessions.title') }}</span>
      <span class="block text-caption text-ink-muted sm:line-clamp-2 xl:line-clamp-1" :title="t('kernel.sessions.hint')">{{ t('kernel.sessions.hint') }}</span>
    </div>
    <JecButton size="sm" class="ml-auto flex-none" @click="show">
      <MonitorSmartphone class="size-4" aria-hidden="true" />{{ t('kernel.sessions.manage') }}
    </JecButton>
  </div>

  <JecDialog v-model:open="open" :title="t('kernel.sessions.title')" fit>
    <div class="flex flex-col gap-3">
      <JecAlert v-if="endError.error.value" :code="endError.error.value.code" :params="endError.error.value.params" />
      <ul class="m-0 list-none p-0">
        <li v-for="s in sessions.data.value" :key="s.id" class="flex flex-wrap items-center gap-2 border-b border-line py-2 last:border-b-0">
          <div class="min-w-0 flex-[1_1_14rem]">
            <span class="flex flex-wrap items-center gap-x-2">
              <span class="font-semibold [overflow-wrap:anywhere]" translate="no" :title="s.userAgent">{{ s.device || s.client || s.id }}</span>
              <span v-if="s.current" class="rounded-sm bg-ok-soft px-1.5 text-caption font-semibold text-ok-ink">{{ t('kernel.sessions.current') }}</span>
            </span>
            <span class="flex flex-wrap gap-x-3 text-caption text-ink-muted">
              <span v-if="s.client" translate="no">{{ s.client }}</span>
              <span v-if="s.via">{{ t(vias[s.via]) }}</span>
              <span>{{ t('kernel.sessions.signed_in') }} <time :datetime="s.createdAt">{{ intl.dateTime(s.createdAt) }}</time></span>
              <span v-if="s.lastSeen">{{ t('kernel.sessions.last_seen') }} <time :datetime="s.lastSeen">{{ intl.dateTime(s.lastSeen) }}</time></span>
            </span>
          </div>
          <JecButton
            v-if="!s.current"
            size="sm"
            variant="ghost"
            :busy="ending === s.id"
            :aria-label="t('kernel.sessions.sign_out_one', { device: s.device || s.client || s.id })"
            @click="signOut(s.id)"
          >
            <LogOut class="size-4" aria-hidden="true" />{{ t('kernel.sessions.sign_out') }}
          </JecButton>
        </li>
      </ul>
    </div>
    <template #footer>
      <JecButton
        v-if="(sessions.data.value?.length ?? 0) > 1"
        variant="danger"
        class="mr-auto"
        :busy="ending === 'others'"
        @click="signOut('others')"
      >
        <LogOut class="size-4" aria-hidden="true" />{{ t('kernel.sessions.sign_out_others') }}
      </JecButton>
      <JecButton @click="open = false">{{ t('kernel.action.close') }}</JecButton>
    </template>
  </JecDialog>
</template>
