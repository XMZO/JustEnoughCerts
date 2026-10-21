<script setup lang="ts">
import { Check, LogIn, Settings2 } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { useApiError } from '../api/errors'
import { callbackURL, useOidcSettings, useSaveOidcSettings } from '../api/oidc'
import { useT } from '../i18n'
import CopyButton from './CopyButton.vue'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'
import JecTextarea from './JecTextarea.vue'

// Single sign-on through an OpenID Connect provider (module oidc): a row of
// the account settings, and the dialog that sets it up. The address the
// provider sends people back to is the page's own, under its base path.
defineProps<{ rowClass: string }>()

const { t } = useT()
const settings = useOidcSettings()
const save = useSaveOidcSettings()
const errors = useApiError(computed(() => save.error.value))
const open = ref(false)
const saved = ref(false)
const callback = callbackURL()

const enabled = ref(false)
const label = ref('')
const issuer = ref('')
const clientId = ref('')
const secret = ref('')
const noSecret = ref(false)
const allowed = ref('')

watch(open, (now) => {
  const s = settings.data.value
  if (!now || !s) return
  save.reset()
  enabled.value = s.enabled || !s.issuer // a first setup means to switch it on
  label.value = s.label
  issuer.value = s.issuer
  clientId.value = s.clientId
  secret.value = ''
  noSecret.value = false
  allowed.value = (s.allowed ?? []).join('\n')
})

async function submit() {
  const body = {
    enabled: enabled.value,
    label: label.value,
    issuer: issuer.value,
    clientId: clientId.value,
    allowed: allowed.value.split('\n').map((a) => a.trim()).filter(Boolean),
    redirectUri: callback,
    // Left empty, the secret saved stays.
    ...(noSecret.value ? { clientSecret: '' } : secret.value ? { clientSecret: secret.value } : {}),
  }
  try {
    await save.mutateAsync(body)
  } catch {
    return // shown by the fields and the alert
  }
  open.value = false
  saved.value = true
}
</script>

<template>
  <div :class="[rowClass, 'flex-wrap']">
    <div class="min-w-0 flex-[1_1_8rem]">
      <span class="block font-semibold">{{ t('oidc.settings.title') }}</span>
      <span class="block text-caption text-ink-muted sm:line-clamp-2 xl:line-clamp-1" :title="t('oidc.settings.hint')">{{ t('oidc.settings.hint') }}</span>
      <span v-if="saved" role="status" class="inline-flex items-center gap-1 text-small text-ok-ink">
        <Check class="size-4 flex-none" aria-hidden="true" />{{ t('oidc.settings.saved') }}
      </span>
    </div>
    <span v-if="settings.data.value?.enabled" class="inline-flex flex-none items-center gap-1 text-small font-semibold text-ok-ink">
      <LogIn class="size-4" aria-hidden="true" />{{ t('oidc.settings.state_on') }}
    </span>
    <span v-else-if="settings.data.value" class="inline-flex flex-none items-center gap-1 text-small text-ink-muted">
      {{ t('oidc.settings.state_off') }}
    </span>
    <JecButton size="sm" class="ml-auto flex-none" :disabled="!settings.data.value" @click="open = true">
      <Settings2 class="size-4" aria-hidden="true" />{{ t('oidc.settings.edit') }}
    </JecButton>
  </div>

  <JecDialog v-model:open="open" :title="t('oidc.settings.title')">
    <form id="oidc-form" class="flex flex-col gap-3" novalidate @submit.prevent="submit">
      <p class="m-0">{{ t('oidc.settings.intro') }}</p>
      <JecAlert v-if="errors.alert.value" :code="errors.alert.value.code" :params="errors.alert.value.params" />
      <label class="flex min-h-9 items-center gap-2.5 font-semibold max-sm:min-h-11">
        <input v-model="enabled" type="checkbox" class="size-4 flex-none accent-brand" />{{ t('oidc.settings.enabled') }}
      </label>
      <JecField id="oidc-issuer" label="oidc.settings.issuer" hint="oidc.settings.issuer_hint" :error="errors.fields.value.issuer">
        <JecInput
          id="oidc-issuer"
          v-model="issuer"
          type="url"
          inputmode="url"
          autocapitalize="none"
          spellcheck="false"
          :invalid="!!errors.fields.value.issuer"
        />
      </JecField>
      <JecField id="oidc-client" label="oidc.settings.client_id" :error="errors.fields.value.clientId">
        <JecInput id="oidc-client" v-model="clientId" autocapitalize="none" spellcheck="false" :invalid="!!errors.fields.value.clientId" />
      </JecField>
      <JecField
        id="oidc-secret"
        label="oidc.settings.client_secret"
        :hint="settings.data.value?.hasSecret ? 'oidc.settings.client_secret_kept' : 'oidc.settings.client_secret_hint'"
        :error="errors.fields.value.clientSecret"
      >
        <JecInput id="oidc-secret" v-model="secret" type="password" autocomplete="off" :disabled="noSecret" />
      </JecField>
      <label v-if="settings.data.value?.hasSecret" class="flex min-h-9 items-center gap-2.5 max-sm:min-h-11">
        <input v-model="noSecret" type="checkbox" class="size-4 flex-none accent-brand" />{{ t('oidc.settings.client_secret_remove') }}
      </label>
      <JecField id="oidc-allowed" label="oidc.settings.allowed" hint="oidc.settings.allowed_hint" :error="errors.fields.value.allowed">
        <JecTextarea id="oidc-allowed" v-model="allowed" :rows="3" :invalid="!!errors.fields.value.allowed" />
      </JecField>
      <JecField id="oidc-label" label="oidc.settings.label" hint="oidc.settings.label_hint" :error="errors.fields.value.label">
        <JecInput id="oidc-label" v-model="label" />
      </JecField>
      <JecField id="oidc-callback" label="oidc.settings.callback" hint="oidc.settings.callback_hint" :error="errors.fields.value.redirectUri">
        <div class="flex min-w-0 items-center gap-2">
          <code id="oidc-callback" class="min-w-0 flex-1 rounded-md bg-surface-sunken px-2 py-1.5 text-small [overflow-wrap:anywhere]" translate="no">{{
            callback
          }}</code>
          <CopyButton :value="callback" label="oidc.settings.callback_copy" />
        </div>
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="oidc-form" variant="primary" :busy="save.isPending.value">{{ t('kernel.action.save') }}</JecButton>
    </template>
  </JecDialog>
</template>
