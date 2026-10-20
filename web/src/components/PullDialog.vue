<script setup lang="ts">
import { KeyRound, PowerOff } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { pullBase, usePull, usePullKey } from '../api/certs'
import { useApiError } from '../api/errors'
import { useT } from '../i18n'
import { useIntl } from '../i18n/intl'
import CopyButton from './CopyButton.vue'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'

// The pull address of a certificate: servers JEC cannot reach fetch its
// version in use themselves, with a key in the Authorization header. A
// key is shown once, with the commands that use it; a new one ends the
// old.
const props = defineProps<{ id: number }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useT()
const intl = useIntl()
const state = usePull(() => props.id, open)
const change = usePullKey(() => props.id)
const error = useApiError(computed(() => change.error.value))
const password = ref('')
const key = ref('')
watch(open, (now) => {
  if (!now) return
  password.value = ''
  key.value = ''
  change.reset()
})
async function makeKey() {
  try {
    key.value = (await change.mutateAsync(password.value))?.key ?? ''
    password.value = ''
  } catch {
    // shown by the field
  }
}
async function turnOff() {
  try {
    await change.mutateAsync(null)
    key.value = ''
  } catch {
    // shown by the alert
  }
}
// What a server runs, as from cron: each file fetched only when changed.
const commands = computed(() =>
  ['fullchain.pem', 'privkey.pem']
    .map((f) => `curl -fsS -H "Authorization: Bearer ${key.value}" --etag-compare ${f}.etag --etag-save ${f}.etag -o ${f} ${pullBase()}${f}`)
    .join('\n'),
)
</script>

<template>
  <JecDialog v-model:open="open" :title="t('certs.pull.title')">
    <form id="pull-form" class="flex flex-col gap-3" novalidate @submit.prevent="makeKey">
      <p class="m-0">{{ t('certs.pull.intro') }}</p>
      <p v-if="state.data.value?.enabled" class="m-0 text-small text-ink-muted">
        {{ t('certs.pull.made') }} <time :datetime="state.data.value.createdAt">{{ intl.dateTime(state.data.value.createdAt ?? '') }}</time>
        <template v-if="state.data.value.usedAt">
          · {{ t('certs.pull.used') }} <time :datetime="state.data.value.usedAt">{{ intl.dateTime(state.data.value.usedAt) }}</time>
        </template>
      </p>
      <JecAlert v-if="error.alert.value" :code="error.alert.value.code" :params="error.alert.value.params" />
      <div v-if="key" class="flex flex-col gap-2">
        <p class="m-0 text-small font-semibold text-ok-ink">{{ t('certs.pull.shown_once') }}</p>
        <div class="flex items-start gap-2 rounded-md bg-surface-sunken p-3">
          <pre class="m-0 min-w-0 flex-1 overflow-x-auto font-mono text-caption whitespace-pre" tabindex="0" translate="no">{{ commands }}</pre>
          <CopyButton :value="commands" label="certs.pull.copy" />
        </div>
        <p class="m-0 text-small text-ink-muted">{{ t('certs.pull.files') }}</p>
      </div>
      <JecField id="pull-password" label="kernel.account.confirm_password" :error="error.fields.value.password">
        <JecInput id="pull-password" v-model="password" type="password" autocomplete="current-password" :invalid="!!error.fields.value.password" />
      </JecField>
    </form>
    <template #footer>
      <JecButton v-if="state.data.value?.enabled" variant="danger" class="mr-auto" :busy="change.isPending.value && !password" @click="turnOff">
        <PowerOff class="size-4" aria-hidden="true" />{{ t('certs.pull.off') }}
      </JecButton>
      <JecButton @click="open = false">{{ t('kernel.action.close') }}</JecButton>
      <JecButton type="submit" form="pull-form" variant="primary" :busy="change.isPending.value && !!password" :disabled="!password">
        <KeyRound class="size-4" aria-hidden="true" />{{ t(state.data.value?.enabled ? 'certs.pull.renew' : 'certs.pull.make') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
