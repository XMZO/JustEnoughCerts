<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useSignIn } from '../api/account'
import { ApiError, networkError } from '../api/client'
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
const resetCommand = 'jec reset-password'

const error = computed(() => {
  const err = signIn.error.value
  if (!err) return null
  return err instanceof ApiError ? err : networkError
})

// Only paths of this app, never another site ("//host" included).
function nextPath(): string {
  const next = route.query.next
  return typeof next === 'string' && next.startsWith('/') && !next.startsWith('//') ? next : '/'
}

async function submit() {
  try {
    await signIn.mutateAsync({ username: username.value, password: password.value })
  } catch {
    return // shown by the alert
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
      <JecButton type="submit" variant="primary" :busy="signIn.isPending.value">{{ t('kernel.login.submit') }}</JecButton>
    </form>
    <template #footer>
      <LanguageSwitch />
      <span>{{ t('kernel.login.forgot') }} <code class="whitespace-nowrap">{{ resetCommand }}</code></span>
    </template>
  </AuthLayout>
</template>
