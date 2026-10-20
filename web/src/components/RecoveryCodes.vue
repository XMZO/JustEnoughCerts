<script setup lang="ts">
import { LifeBuoy } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { useNewRecoveryCodes } from '../api/account'
import { useApiError } from '../api/errors'
import { useT } from '../i18n'
import CopyButton from './CopyButton.vue'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'

// The recovery codes of two-factor authentication: a row of the account
// settings with how many are left, the dialog that makes new ones once the
// password is confirmed, and the one that shows codes just made, as
// turning two-factor authentication on does too (v-model:codes).
defineProps<{ rowClass: string; left: number }>()
const codes = defineModel<string[] | undefined>('codes')

const { t } = useT()
const making = ref(false)
const password = ref('')
const make = useNewRecoveryCodes()
const makeError = useApiError(computed(() => make.error.value))
watch(making, (open) => {
  if (!open) return
  password.value = ''
  make.reset()
})
async function submit() {
  try {
    const out = await make.mutateAsync({ password: password.value })
    making.value = false
    codes.value = out.recoveryCodes
  } catch {
    // shown by the field
  }
}
const shown = computed({
  get: () => !!codes.value,
  set: (open) => {
    if (!open) codes.value = undefined
  },
})
</script>

<template>
  <div :class="[rowClass, 'flex-wrap']">
    <div class="min-w-0 flex-[1_1_12rem]">
      <span class="block font-semibold">{{ t('kernel.recovery.title') }}</span>
      <span :class="['block text-caption leading-[18px]', left <= 2 ? 'font-semibold text-warn-ink' : 'text-ink-muted']">
        {{ t('kernel.recovery.left', { count: left }) }}
      </span>
    </div>
    <JecButton size="sm" class="ml-auto flex-none" @click="making = true">
      <LifeBuoy class="size-4" aria-hidden="true" />{{ t('kernel.recovery.new') }}
    </JecButton>
  </div>

  <JecDialog v-model:open="making" :title="t('kernel.recovery.new')" small>
    <form id="recovery-form" class="flex flex-col gap-3" novalidate @submit.prevent="submit">
      <p class="m-0">{{ t('kernel.recovery.new_intro') }}</p>
      <JecAlert v-if="makeError.alert.value" :code="makeError.alert.value.code" :params="makeError.alert.value.params" />
      <JecField id="recovery-password" label="kernel.account.confirm_password" :error="makeError.fields.value.password">
        <JecInput
          id="recovery-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!makeError.fields.value.password"
        />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="making = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="recovery-form" variant="primary" :busy="make.isPending.value" :disabled="!password">
        {{ t('kernel.recovery.make') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="shown" :title="t('kernel.recovery.title')" small>
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('kernel.recovery.intro') }}</p>
      <div class="flex items-start gap-2 rounded-md bg-surface-sunken p-3">
        <ol class="m-0 grid flex-1 list-none grid-cols-2 gap-x-4 gap-y-1 p-0 font-mono" translate="no">
          <li v-for="c in codes" :key="c" class="select-all">{{ c }}</li>
        </ol>
        <CopyButton v-if="codes" :value="codes.join('\n')" label="kernel.recovery.copy" />
      </div>
    </div>
    <template #footer>
      <JecButton variant="primary" @click="shown = false">{{ t('kernel.recovery.saved') }}</JecButton>
    </template>
  </JecDialog>
</template>
