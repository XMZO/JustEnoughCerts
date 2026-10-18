<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useSignIn } from '../api/account'
import { ApiError } from '../api/client'
import { useApiError } from '../api/errors'
import AuthLayout from '../components/AuthLayout.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import LanguageSwitch from '../components/LanguageSwitch.vue'
import { useT } from '../i18n'

const { t } = useT()
const route = useRoute()
const router = useRouter()
const signIn = useSignIn()

const username = ref('')
const password = ref('')
// The code of the authenticator app, asked for once the password proves
// right for an account with two-factor authentication on.
const code = ref('')
const needsCode = ref(false)
const resetCommand = 'jec reset-password'

const failure = useApiError(computed(() => signIn.error.value))
const error = computed(() => {
  const err = failure.error.value
  return err && err.code !== 'kernel.error.two_factor_required' && !failure.fields.value.code ? err : null
})

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
      <JecField id="username" label="kernel.account.username">
        <JecInput id="username" v-model="username" autocomplete="username" autocapitalize="none" spellcheck="false" required />
      </JecField>
      <JecField id="password" label="kernel.account.password">
        <JecInput id="password" v-model="password" type="password" autocomplete="current-password" required />
      </JecField>
      <JecField v-if="needsCode" id="code" label="kernel.login.code" hint="kernel.login.code_hint" :error="failure.fields.value.code">
        <JecInput
          id="code"
          v-model="code"
          inputmode="numeric"
          autocomplete="one-time-code"
          maxlength="7"
          required
          :invalid="!!failure.fields.value.code"
          :aria-describedby="failure.fields.value.code ? 'code-error' : 'code-hint'"
        />
      </JecField>
      <JecButton type="submit" variant="primary" :busy="signIn.isPending.value">{{ t('kernel.login.submit') }}</JecButton>
    </form>
    <template #footer>
      <LanguageSwitch />
      <span>{{ t('kernel.login.forgot') }} <code class="whitespace-nowrap">{{ resetCommand }}</code></span>
    </template>
  </AuthLayout>
</template>
