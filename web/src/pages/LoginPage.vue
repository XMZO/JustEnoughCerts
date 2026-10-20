<script setup lang="ts">
import { LogIn } from '@lucide/vue'
import { computed, nextTick, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useSignIn } from '../api/account'
import { ApiError } from '../api/client'
import { useApiError } from '../api/errors'
import { loginURL, useOidcStatus } from '../api/oidc'
import AuthLayout from '../components/AuthLayout.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import LanguageSwitch from '../components/LanguageSwitch.vue'
import { useT, type MessageKey } from '../i18n'

const { t } = useT()
const route = useRoute()
const router = useRouter()
const signIn = useSignIn()

const username = ref('')
const password = ref('')
// The code of the authenticator app, asked for once the password proves
// right for an account with two-factor authentication on.
const code = ref('')
// A recovery code in place of the app's, for a lost authenticator.
const recovery = ref(false)
function switchCode() {
  recovery.value = !recovery.value
  code.value = ''
  document.getElementById('code')?.focus()
}
const needsCode = ref(false)
const resetCommand = 'jec reset-password'

const failure = useApiError(computed(() => signIn.error.value))
const error = computed(() => {
  const err = failure.error.value
  return err && err.code !== 'kernel.error.two_factor_required' && !failure.fields.value.code ? err : null
})

// Single sign-on, when the oidc module offers it, and why one came back
// without a session.
const sso = useOidcStatus()
const ssoReasons: Record<string, MessageKey> = {
  expired: 'oidc.login.expired',
  denied: 'oidc.login.denied',
  invalid: 'oidc.login.invalid',
  not_allowed: 'oidc.login.not_allowed',
  no_account: 'oidc.login.no_account',
  off: 'oidc.login.off',
}
const ssoError = computed(() => (typeof route.query.sso === 'string' ? ssoReasons[route.query.sso] : undefined))
function signInElsewhere() {
  window.location.assign(loginURL())
}

// Only paths of this app, never another site ("//host" included).
function nextPath(): string {
  const next = route.query.next
  return typeof next === 'string' && next.startsWith('/') && !next.startsWith('//') ? next : '/'
}

async function submit() {
  try {
    await signIn.mutateAsync({ username: username.value, password: password.value, ...(needsCode.value ? { code: code.value } : {}) })
  } catch (err) {
    if (err instanceof ApiError && err.code === 'kernel.error.two_factor_required') {
      needsCode.value = true
      await nextTick()
      document.getElementById('code')?.focus()
    }
    return // shown by the alert, or by the field of the code
  }
  await router.replace(nextPath())
}
</script>

<template>
  <AuthLayout>
    <form class="flex flex-col gap-3.5 rounded-lg border border-line bg-surface-raised p-5" novalidate @submit.prevent="submit">
      <JecAlert v-if="error" :code="error.code" :params="error.params" />
      <JecAlert v-else-if="ssoError" :code="ssoError" />
      <JecField id="username" label="kernel.account.username">
        <JecInput id="username" v-model="username" autocomplete="username" autocapitalize="none" spellcheck="false" required />
      </JecField>
      <JecField id="password" label="kernel.account.password">
        <JecInput id="password" v-model="password" type="password" autocomplete="current-password" required />
      </JecField>
      <JecField
        v-if="needsCode"
        id="code"
        :label="recovery ? 'kernel.login.recovery' : 'kernel.login.code'"
        :hint="recovery ? 'kernel.login.recovery_hint' : 'kernel.login.code_hint'"
        :error="failure.fields.value.code"
      >
        <JecInput
          id="code"
          v-model="code"
          :inputmode="recovery ? 'text' : 'numeric'"
          :autocomplete="recovery ? 'off' : 'one-time-code'"
          :maxlength="recovery ? 12 : 7"
          autocapitalize="none"
          spellcheck="false"
          required
          :invalid="!!failure.fields.value.code"
          :aria-describedby="failure.fields.value.code ? 'code-error' : 'code-hint'"
        />
      </JecField>
      <button
        v-if="needsCode"
        type="button"
        class="-mt-2 cursor-pointer self-start border-0 bg-transparent p-0 text-small text-ink-muted underline hover:text-ink max-sm:min-h-11"
        @click="switchCode"
      >
        {{ t(recovery ? 'kernel.login.use_code' : 'kernel.login.use_recovery') }}
      </button>
      <JecButton type="submit" variant="primary" :busy="signIn.isPending.value">{{ t('kernel.login.submit') }}</JecButton>
      <template v-if="sso.data.value?.enabled">
        <div class="flex items-center gap-2 text-small text-ink-muted" aria-hidden="true">
          <span class="h-px flex-1 bg-line" />{{ t('oidc.login.or') }}<span class="h-px flex-1 bg-line" />
        </div>
        <JecButton @click="signInElsewhere">
          <LogIn class="size-4" aria-hidden="true" />{{ t('oidc.login.button', { provider: sso.data.value.label }) }}
        </JecButton>
      </template>
    </form>
    <template #footer>
      <LanguageSwitch />
      <span>{{ t('kernel.login.forgot') }} <code class="whitespace-nowrap">{{ resetCommand }}</code></span>
    </template>
  </AuthLayout>
</template>
