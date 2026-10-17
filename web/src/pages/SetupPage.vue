<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useSetup } from '../api/account'
import { ApiError, networkError } from '../api/client'
import AuthLayout from '../components/AuthLayout.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import LanguageSwitch from '../components/LanguageSwitch.vue'
import { useT, type MessageKey } from '../i18n'

const { t, locale } = useT()
const router = useRouter()
const setup = useSetup()

const token = ref('')
const username = ref('')
const password = ref('')
const again = ref('')
const triedSubmit = ref(false)
const minPassword = 8 // auth.MinPasswordLength
const logsCommand = 'docker logs jec'

const mismatch = computed(() => (triedSubmit.value || again.value.length >= password.value.length) && again.value !== password.value)

const error = computed(() => {
  const err = setup.error.value
  if (!err) return null
  return err instanceof ApiError ? err : networkError
})

// The fields the server found at fault, by field name.
const fieldErrors = computed(() => {
  const out: Record<string, MessageKey> = {}
  for (const f of error.value?.fields ?? []) out[f.location.replace(/^body\./, '')] = f.code
  return out
})

async function submit() {
  triedSubmit.value = true
  if (mismatch.value) return
  try {
    await setup.mutateAsync({ token: token.value, username: username.value, password: password.value, locale: locale.value })
  } catch {
    return // shown by the alert
  }
  await router.replace('/')
}
</script>

<template>
  <AuthLayout>
    <form class="flex flex-col gap-3.5 rounded-lg border border-line bg-surface-raised p-5" novalidate @submit.prevent="submit">
      <div class="flex flex-col gap-2">
        <h2 class="m-0 text-section-title font-semibold">{{ t('kernel.setup.title') }}</h2>
        <p class="m-0 text-ink-muted">{{ t('kernel.setup.intro') }}</p>
        <code class="block rounded-sm bg-surface-sunken px-2 py-1">{{ logsCommand }}</code>
      </div>
      <JecAlert v-if="error && !error.fields.length" :code="error.code" :params="error.params" />
      <JecField id="token" label="kernel.setup.token" :error="fieldErrors.token">
        <JecInput id="token" v-model="token" mono autocomplete="off" autocapitalize="none" spellcheck="false" required />
      </JecField>
      <JecField id="username" label="kernel.account.username" :error="fieldErrors.username">
        <JecInput id="username" v-model="username" autocomplete="username" autocapitalize="none" spellcheck="false" required />
      </JecField>
      <JecField
        id="password"
        label="kernel.account.password"
        hint="kernel.account.password_hint"
        :hint-params="{ min: minPassword }"
        :error="fieldErrors.password"
      >
        <JecInput id="password" v-model="password" type="password" autocomplete="new-password" :minlength="minPassword" required />
      </JecField>
      <JecField id="again" label="kernel.account.password_again" :error="mismatch ? 'kernel.account.password_mismatch' : null">
        <JecInput id="again" v-model="again" type="password" autocomplete="new-password" :invalid="mismatch" required />
      </JecField>
      <JecButton type="submit" variant="primary" :busy="setup.isPending.value">{{ t('kernel.setup.submit') }}</JecButton>
    </form>
    <template #footer>
      <LanguageSwitch />
    </template>
  </AuthLayout>
</template>
