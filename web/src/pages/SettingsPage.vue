<script setup lang="ts">
import { Check, Download, KeyRound, KeySquare, ShieldCheck, ShieldOff, Trash2 } from '@lucide/vue'
import { computed, ref, watch, watchEffect } from 'vue'

import {
  useChangePassword,
  useCreateToken,
  useDeleteToken,
  useDisableTwoFactor,
  useEnableTwoFactor,
  useSession,
  useTokens,
  useTwoFactorSecret,
  useUpdateAccount,
} from '../api/account'
import { saveFile, useCertSettings, useSaveCertSettings, type CertSettingsInput, type KeyType } from '../api/certs'
import { useApiError } from '../api/errors'
import { useManifest } from '../api/manifest'
import { useBackup, useSystem } from '../api/system'
import { keyText } from '../certs/life'
import CopyButton from '../components/CopyButton.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import PageHeader from '../components/PageHeader.vue'
import { locales, setLocale, useT, type Locale } from '../i18n'
import { useIntl } from '../i18n/intl'

// The settings (docs/design/boards/Settings and SettingsMobile), each saved
// as soon as it changes. Those of the account, the panel's address and the
// backups come later.
const { t, locale } = useT()
const intl = useIntl()
const manifest = useManifest()

// The defaults of new certificates and the time renewals start, of the
// certs module.
const settings = useCertSettings()
const save = useSaveCertSettings()
const loadError = useApiError(computed(() => settings.error.value))
const saveError = useApiError(computed(() => save.error.value))
const form = ref<CertSettingsInput>()
watchEffect(() => {
  const st = settings.data.value
  if (st && !form.value) form.value = { renewBeforeDays: st.renewBeforeDays, keyType: st.keyType, checkTime: st.checkTime }
})
const keyTypes: KeyType[] = ['rsa-2048', 'ecdsa-p256', 'ecdsa-p384', 'rsa-3072', 'rsa-4096']
const keyLabel = (k: string) => {
  const text = keyText(k)
  return t(text.key, text.params)
}
const saved = computed(() => save.isSuccess.value && !save.isPending.value)

async function store() {
  if (!form.value) return
  try {
    await save.mutateAsync({ ...form.value })
  } catch {
    // shown by the fields
  }
}

// The account: its password, changed in a dialog once the current one is
// confirmed, which signs it out everywhere else.
const session = useSession()
const change = useChangePassword()
const changeError = useApiError(computed(() => change.error.value))
const changing = ref(false)
const changed = ref(false)
const pw = ref({ current: '', next: '', again: '' })
const mismatch = ref(false)
watch(changing, (open) => {
  if (!open) return
  pw.value = { current: '', next: '', again: '' }
  mismatch.value = false
  change.reset()
})

async function submitPassword() {
  mismatch.value = pw.value.next !== pw.value.again
  if (mismatch.value) return
  try {
    await change.mutateAsync({ password: pw.value.current, newPassword: pw.value.next })
  } catch {
    return // shown by the fields
  }
  changing.value = false
  changed.value = true
}

// Two-factor authentication: turned on in a dialog of two steps, the
// password and then a code of the new secret, which signs the account out
// everywhere else; turned off once the password is confirmed. And how many
// days a sign-in lasts, saved as soon as it changes.
const secret = useTwoFactorSecret()
const enable = useEnableTwoFactor()
const disable = useDisableTwoFactor()
const updateAccount = useUpdateAccount()
const secretError = useApiError(computed(() => secret.error.value))
const enableError = useApiError(computed(() => enable.error.value))
const disableError = useApiError(computed(() => disable.error.value))
const accountError = useApiError(computed(() => updateAccount.error.value))
const turningOn = ref(false)
const turningOff = ref(false)
const twoFactorDone = ref<'on' | 'off'>()
const tfPassword = ref('')
const tfCode = ref('')
const resetCommand = 'jec reset-password'
watch(turningOn, (open) => {
  if (!open) return
  tfPassword.value = ''
  tfCode.value = ''
  secret.reset()
  enable.reset()
})
watch(turningOff, (open) => {
  if (!open) return
  tfPassword.value = ''
  disable.reset()
})
// The key in groups of four, as people type it.
const groupedKey = computed(() => secret.data.value?.secret.match(/.{1,4}/g)?.join(' ') ?? '')

async function submitTurnOn() {
  try {
    if (!secret.data.value) {
      await secret.mutateAsync({ password: tfPassword.value })
      return // the QR code shows next
    }
    await enable.mutateAsync({ code: tfCode.value })
  } catch {
    // A secret that waited too long goes: the dialog starts over.
    if (enableError.error.value?.code === 'kernel.error.two_factor_expired') secret.reset()
    return // shown by the fields
  }
  turningOn.value = false
  twoFactorDone.value = 'on'
}

async function submitTurnOff() {
  try {
    await disable.mutateAsync({ password: tfPassword.value })
  } catch {
    return // shown by the field
  }
  turningOff.value = false
  twoFactorDone.value = 'off'
}

const sessionDays = [1, 7, 30] as const
async function pickSessionDays(e: Event) {
  try {
    await updateAccount.mutateAsync({ sessionDays: Number((e.target as HTMLSelectElement).value) as (typeof sessionDays)[number] })
  } catch {
    // shown above the settings of the account
  }
}

// API tokens: listed and made in a dialog, a new one's value shown once,
// and deleted at a second click.
const tokensOpen = ref(false)
const tokens = useTokens(computed(() => !!session.data.value))
const createToken = useCreateToken()
const deleteToken = useDeleteToken()
const tokenError = useApiError(computed(() => createToken.error.value))
const deleteTokenError = useApiError(computed(() => deleteToken.error.value))
const emptyToken = () => ({ name: '', scope: 'read' as 'read' | 'write', password: '' })
const tokenForm = ref(emptyToken())
const madeToken = ref<{ name: string; token: string }>()
const confirmDelete = ref<number>()
const scopeText = { read: 'kernel.tokens.scope_read', write: 'kernel.tokens.scope_write' } as const
watch(tokensOpen, (open) => {
  if (!open) return
  tokenForm.value = emptyToken()
  madeToken.value = undefined
  confirmDelete.value = undefined
  createToken.reset()
  deleteToken.reset()
})

async function submitToken() {
  let made
  try {
    made = await createToken.mutateAsync({ ...tokenForm.value, name: tokenForm.value.name.trim() })
  } catch {
    return // shown by the fields
  }
  madeToken.value = { name: made.detail.name, token: made.token }
  tokenForm.value = emptyToken()
}

async function removeToken(id: number) {
  if (confirmDelete.value !== id) {
    confirmDelete.value = id
    return
  }
  try {
    await deleteToken.mutateAsync(id)
  } catch {
    return // shown above the list
  }
  confirmDelete.value = undefined
}

// How JEC is reached and where its data is, set outside the panel; and a
// copy of the database, once the password is confirmed.
const system = useSystem()
const backup = useBackup()
const backupError = useApiError(computed(() => backup.error.value))
const backingUp = ref(false)
const backupPassword = ref('')
watch(backingUp, (open) => {
  if (!open) return
  backupPassword.value = ''
  backup.reset()
})

async function submitBackup() {
  let file
  try {
    file = await backup.mutateAsync(backupPassword.value)
  } catch {
    return // shown by the field
  }
  saveFile(file.blob, file.name)
  backingUp.value = false
}

function pickLocale(e: Event) {
  void setLocale((e.target as HTMLSelectElement).value as Locale)
}

// The repository's name is data, not words to translate.
const repository = { name: 'XMZO/JustEnoughCerts', url: 'https://github.com/XMZO/JustEnoughCerts' }

const panel = 'flex flex-col rounded-lg border border-line bg-surface-raised px-3 py-2 sm:p-4'
const row = 'flex min-h-[52px] items-center gap-3 border-b border-line py-2 last:border-b-0'
const control = 'h-9 rounded-md border border-line-strong bg-surface-raised px-2 text-ink aria-invalid:border-danger-ink max-sm:h-11'
</script>

<template>
  <PageHeader :title="t('kernel.settings.title')" :subtitle="t('kernel.settings.intro')" />

  <div class="grid items-start gap-3 px-4 pt-3 sm:mt-4 sm:grid-cols-[repeat(auto-fill,minmax(420px,1fr))] sm:gap-4 sm:p-0">
    <p class="m-0 text-ink-muted sm:hidden">{{ t('kernel.settings.intro') }}</p>

    <section aria-labelledby="appearance" :class="panel">
      <h2 id="appearance" class="m-0 mb-1 text-heading font-semibold sm:text-section-title">{{ t('kernel.settings.appearance') }}</h2>
      <div :class="row">
        <div class="min-w-0 flex-1">
          <label for="language" class="block font-semibold">{{ t('kernel.settings.language') }}</label>
          <span id="language-help" class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.language_help') }}</span>
        </div>
        <select id="language" :value="locale" aria-describedby="language-help" :class="[control, 'flex-none']" @change="pickLocale">
          <option v-for="l in locales" :key="l.tag" :value="l.tag" :lang="l.tag" translate="no">{{ l.name }}</option>
        </select>
      </div>
    </section>

    <section aria-labelledby="renewal" :class="panel">
      <div class="mb-1 flex flex-wrap items-center justify-between gap-x-3">
        <h2 id="renewal" class="m-0 text-heading font-semibold sm:text-section-title">{{ t('certs.defaults.title') }}</h2>
        <p v-if="saved" role="status" class="m-0 inline-flex items-center gap-1 text-small text-ok-ink">
          <Check class="size-4" aria-hidden="true" />{{ t('kernel.settings.saved') }}
        </p>
      </div>
      <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />
      <JecAlert v-if="saveError.alert.value" :code="saveError.alert.value.code" :params="saveError.alert.value.params" />
      <template v-if="form">
        <div :class="row">
          <div class="min-w-0 flex-1">
            <label for="renewBeforeDays" class="block font-semibold">{{ t('certs.defaults.days') }}</label>
            <span id="days-help" class="block text-caption leading-[18px] text-ink-muted">
              {{ saveError.fields.value.renewBeforeDays ? t(saveError.fields.value.renewBeforeDays) : t('certs.defaults.days_help') }}
            </span>
          </div>
          <span class="flex flex-none items-center gap-2">
            <input
              id="renewBeforeDays"
              v-model.number="form.renewBeforeDays"
              type="number"
              inputmode="numeric"
              min="1"
              max="365"
              aria-describedby="days-help"
              :aria-invalid="!!saveError.fields.value.renewBeforeDays || undefined"
              :class="[control, 'w-20 tabular-nums']"
              @change="store"
            />
            <span>{{ t('certs.settings.days_unit') }}</span>
          </span>
        </div>
        <div :class="row">
          <div class="min-w-0 flex-1">
            <label for="checkTime" class="block font-semibold">{{ t('certs.defaults.check_time') }}</label>
            <span id="check-help" class="block text-caption leading-[18px] text-ink-muted">
              {{
                saveError.fields.value.checkTime
                  ? t(saveError.fields.value.checkTime)
                  : t('certs.defaults.check_time_help', { zone: settings.data.value?.timeZone ?? '' })
              }}
            </span>
          </div>
          <input
            id="checkTime"
            v-model="form.checkTime"
            type="time"
            required
            aria-describedby="check-help"
            :aria-invalid="!!saveError.fields.value.checkTime || undefined"
            :class="[control, 'w-28 flex-none tabular-nums']"
            @change="store"
          />
        </div>
        <div :class="row">
          <div class="min-w-0 flex-1">
            <label for="keyType" class="block font-semibold">{{ t('certs.defaults.key_type') }}</label>
            <span id="key-help" class="block text-caption leading-[18px] text-ink-muted">{{ t('certs.defaults.key_type_help') }}</span>
          </div>
          <select id="keyType" v-model="form.keyType" aria-describedby="key-help" :class="[control, 'flex-none']" @change="store">
            <option v-for="k in keyTypes" :key="k" :value="k">{{ keyLabel(k) }}</option>
          </select>
        </div>
      </template>
    </section>

    <section v-if="session.data.value" aria-labelledby="account" :class="panel">
      <h2 id="account" class="m-0 mb-1 text-heading font-semibold sm:text-section-title">{{ t('kernel.settings.account') }}</h2>
      <p v-if="changed" role="status" class="m-0 mb-1 inline-flex items-center gap-1 text-small text-ok-ink">
        <Check class="size-4 flex-none" aria-hidden="true" />{{ t('kernel.settings.password_done') }}
      </p>
      <p v-if="twoFactorDone" role="status" class="m-0 mb-1 inline-flex items-center gap-1 text-small text-ok-ink">
        <Check class="size-4 flex-none" aria-hidden="true" />{{
          t(twoFactorDone === 'on' ? 'kernel.settings.two_factor_done' : 'kernel.settings.two_factor_off_done')
        }}
      </p>
      <JecAlert v-if="accountError.error.value" :code="accountError.error.value.code" :params="accountError.error.value.params" />
      <div :class="row">
        <span class="min-w-0 flex-1 font-semibold">{{ t('kernel.account.username') }}</span>
        <span class="min-w-0 max-w-[60%] text-right [overflow-wrap:anywhere]" translate="no">{{ session.data.value.username }}</span>
      </div>
      <div :class="[row, 'flex-wrap']">
        <div class="min-w-0 flex-[1_1_12rem]">
          <span class="block font-semibold">{{ t('kernel.account.password') }}</span>
          <span v-if="session.data.value.passwordChangedAt" class="block text-caption leading-[18px] text-ink-muted">
            {{ t('kernel.settings.password_changed_at', { date: intl.date(session.data.value.passwordChangedAt) }) }}
          </span>
        </div>
        <JecButton size="sm" class="ml-auto flex-none" @click="changing = true">
          <KeyRound class="size-4" aria-hidden="true" />{{ t('kernel.settings.change_password') }}
        </JecButton>
      </div>
      <div :class="[row, 'flex-wrap']">
        <div class="min-w-0 flex-[1_1_12rem]">
          <span class="block font-semibold">{{ t('kernel.settings.two_factor') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.two_factor_hint') }}</span>
        </div>
        <span v-if="session.data.value.twoFactor" class="inline-flex flex-none items-center gap-1 text-small font-semibold text-ok-ink">
          <ShieldCheck class="size-4" aria-hidden="true" />{{ t('kernel.settings.two_factor_state_on') }}
        </span>
        <span v-else class="inline-flex flex-none items-center gap-1 text-small text-ink-muted">
          <ShieldOff class="size-4" aria-hidden="true" />{{ t('kernel.settings.two_factor_state_off') }}
        </span>
        <JecButton v-if="session.data.value.twoFactor" size="sm" class="ml-auto flex-none" @click="turningOff = true">
          {{ t('kernel.settings.two_factor_turn_off') }}
        </JecButton>
        <JecButton v-else size="sm" class="ml-auto flex-none" @click="turningOn = true">
          {{ t('kernel.settings.two_factor_turn_on') }}
        </JecButton>
      </div>
      <div :class="row">
        <div class="min-w-0 flex-1">
          <label for="session-days" class="block font-semibold">{{ t('kernel.settings.session_days') }}</label>
          <span id="session-days-help" class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.session_days_hint') }}</span>
        </div>
        <select
          id="session-days"
          :value="session.data.value.sessionDays"
          aria-describedby="session-days-help"
          :aria-invalid="accountError.error.value ? 'true' : undefined"
          :class="[control, 'flex-none']"
          @change="pickSessionDays"
        >
          <option v-for="d in sessionDays" :key="d" :value="d">{{ t('kernel.settings.days', { days: d }) }}</option>
        </select>
      </div>
      <div :class="[row, 'flex-wrap']">
        <div class="min-w-0 flex-[1_1_12rem]">
          <span class="block font-semibold">{{ t('kernel.settings.tokens') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.tokens_hint') }}</span>
        </div>
        <data v-if="tokens.data.value" :value="tokens.data.value.tokens.length" class="flex-none text-small text-ink-muted tabular-nums">
          {{ tokens.data.value.tokens.length }}
        </data>
        <JecButton size="sm" class="ml-auto flex-none" @click="tokensOpen = true">
          <KeySquare class="size-4" aria-hidden="true" />{{ t('kernel.settings.tokens_manage') }}
        </JecButton>
      </div>
    </section>

    <section v-if="system.data.value" aria-labelledby="access" :class="panel">
      <h2 id="access" class="m-0 mb-1 text-heading font-semibold sm:text-section-title">{{ t('kernel.settings.access') }}</h2>
      <div :class="row">
        <div class="min-w-0 flex-1">
          <span class="block font-semibold">{{ t('kernel.settings.base_url') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.base_url_help') }}</span>
        </div>
        <span v-if="system.data.value.baseURL" class="min-w-0 max-w-[60%] text-right font-mono text-small [overflow-wrap:anywhere]" translate="no">
          {{ system.data.value.baseURL }}
        </span>
        <span v-else class="flex-none text-ink-muted">{{ t('kernel.settings.not_set') }}</span>
      </div>
      <div :class="row">
        <div class="min-w-0 flex-1">
          <span class="block font-semibold">{{ t('kernel.settings.listen') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.listen_help') }}</span>
        </div>
        <span class="min-w-0 max-w-[60%] text-right font-mono text-small [overflow-wrap:anywhere]" translate="no">{{ system.data.value.listen }}</span>
      </div>
      <div :class="row">
        <div class="min-w-0 flex-1">
          <span class="block font-semibold">{{ t('kernel.settings.trusted_proxies') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.trusted_proxies_help') }}</span>
        </div>
        <span
          v-if="system.data.value.trustedProxies"
          class="min-w-0 max-w-[60%] text-right font-mono text-small [overflow-wrap:anywhere]"
          translate="no"
        >
          {{ system.data.value.trustedProxies }}
        </span>
        <span v-else class="flex-none text-ink-muted">{{ t('kernel.settings.not_set') }}</span>
      </div>
      <div :class="row">
        <div class="min-w-0 flex-1">
          <span class="block font-semibold">{{ t('kernel.settings.hsts') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.hsts_help') }}</span>
        </div>
        <span :class="['flex-none', system.data.value.hsts ? 'font-semibold text-ok-ink' : 'text-ink-muted']">{{
          t(system.data.value.hsts ? 'kernel.settings.hsts_on' : 'kernel.settings.hsts_off')
        }}</span>
      </div>
    </section>

    <section v-if="system.data.value" aria-labelledby="data" :class="panel">
      <h2 id="data" class="m-0 mb-1 text-heading font-semibold sm:text-section-title">{{ t('kernel.settings.data') }}</h2>
      <div :class="row">
        <div class="min-w-0 flex-1">
          <span class="block font-semibold">{{ t('kernel.settings.data_dir') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.data_dir_help') }}</span>
        </div>
        <span class="min-w-0 max-w-[60%] text-right font-mono text-small [overflow-wrap:anywhere]" translate="no">{{ system.data.value.dataDir }}</span>
      </div>
      <div :class="[row, 'flex-wrap']">
        <div class="min-w-0 flex-[1_1_12rem]">
          <span class="block font-semibold">{{ t('kernel.settings.backup') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.backup_help') }}</span>
        </div>
        <JecButton size="sm" class="ml-auto flex-none" @click="backingUp = true">
          <Download class="size-4" aria-hidden="true" />{{ t('kernel.settings.backup_download') }}
        </JecButton>
      </div>
      <div :class="row">
        <div class="min-w-0 flex-1">
          <span class="block font-semibold">{{ t('kernel.settings.secret_key') }}</span>
          <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.secret_key_help') }}</span>
        </div>
        <span class="min-w-0 max-w-[60%] text-right text-small">
          {{ t(system.data.value.secretKey === 'env' ? 'kernel.settings.secret_key_env' : 'kernel.settings.secret_key_file') }}
        </span>
      </div>
    </section>

    <section v-if="manifest.data.value" aria-labelledby="about" :class="panel">
      <h2 id="about" class="m-0 mb-1 text-heading font-semibold sm:text-section-title">{{ t('kernel.settings.about') }}</h2>
      <dl class="m-0">
        <!-- A build from source has a long version: it wraps. -->
        <div :class="row">
          <dt class="flex-none font-semibold">{{ t('kernel.settings.version') }}</dt>
          <dd class="m-0 min-w-0 flex-1 text-right font-mono text-small [overflow-wrap:anywhere]">
            <data :value="manifest.data.value.version">{{ manifest.data.value.version }}</data>
          </dd>
        </div>
        <div :class="row">
          <dt class="flex-none font-semibold">{{ t('kernel.settings.build') }}</dt>
          <dd class="m-0 flex min-w-0 flex-1 flex-wrap justify-end gap-x-2 text-small text-ink-muted">
            <time v-if="manifest.data.value.date" :datetime="manifest.data.value.date">{{ intl.date(manifest.data.value.date) }}</time>
            <data :value="manifest.data.value.platform" class="font-mono">{{ manifest.data.value.platform }}</data>
          </dd>
        </div>
        <div :class="row">
          <dt class="min-w-0 flex-1">
            <span class="block font-semibold">{{ t('kernel.settings.source') }}</span>
            <span class="block text-caption leading-[18px] text-ink-muted">{{ t('kernel.settings.license') }}</span>
          </dt>
          <dd class="m-0 flex-none">
            <a :href="repository.url" target="_blank" rel="noopener" translate="no">{{ repository.name }}</a>
          </dd>
        </div>
      </dl>
    </section>
  </div>

  <JecDialog v-model:open="backingUp" :title="t('kernel.settings.backup_download')" small>
    <form id="backup-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitBackup">
      <p class="m-0">{{ t('kernel.settings.backup_intro') }}</p>
      <JecAlert v-if="backupError.alert.value" :code="backupError.alert.value.code" :params="backupError.alert.value.params" />
      <JecField id="backup-password" label="kernel.account.confirm_password" :error="backupError.fields.value.password">
        <JecInput
          id="backup-password"
          v-model="backupPassword"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!backupError.fields.value.password"
        />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="backingUp = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="backup-form" variant="primary" :busy="backup.isPending.value" :disabled="!backupPassword">
        <Download class="size-4" aria-hidden="true" />{{ t('kernel.settings.backup_download') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="tokensOpen" :title="t('kernel.tokens.title')">
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('kernel.tokens.intro') }}</p>
      <div v-if="madeToken" role="status" class="flex flex-col gap-2 rounded-md bg-ok-soft p-3">
        <p class="m-0 text-small font-semibold text-ok-ink">{{ t('kernel.tokens.made_new', { name: madeToken.name }) }}</p>
        <div class="flex items-center gap-2">
          <code class="min-w-0 flex-1 rounded-sm bg-surface-raised px-2 py-1 text-small [overflow-wrap:anywhere]">{{ madeToken.token }}</code>
          <CopyButton :value="madeToken.token" label="kernel.tokens.copy" />
        </div>
      </div>
      <JecAlert v-if="deleteTokenError.error.value" :code="deleteTokenError.error.value.code" :params="deleteTokenError.error.value.params" />
      <p v-if="tokens.data.value && !tokens.data.value.tokens.length" class="m-0 text-ink-muted">{{ t('kernel.tokens.empty') }}</p>
      <ul v-else class="m-0 list-none p-0">
        <li v-for="tk in tokens.data.value?.tokens" :key="tk.id" class="flex flex-wrap items-center gap-2 border-b border-line py-2 last:border-b-0">
          <div class="min-w-0 flex-[1_1_12rem]">
            <span class="block font-semibold [overflow-wrap:anywhere]" translate="no">{{ tk.name }}</span>
            <span class="flex flex-wrap gap-x-2 text-caption text-ink-muted">
              <span class="font-semibold">{{ t(scopeText[tk.scope]) }}</span>
              <span>{{ t('kernel.tokens.made', { date: intl.date(tk.createdAt) }) }}</span>
              <span>{{ tk.lastUsedAt ? t('kernel.tokens.used', { date: intl.dateTime(tk.lastUsedAt) }) : t('kernel.tokens.never_used') }}</span>
            </span>
          </div>
          <JecButton
            size="sm"
            class="ml-auto flex-none"
            :variant="confirmDelete === tk.id ? 'danger' : 'ghost'"
            :busy="deleteToken.isPending.value && confirmDelete === tk.id"
            :aria-label="t('kernel.tokens.delete', { name: tk.name })"
            :title="t('kernel.tokens.delete', { name: tk.name })"
            @click="removeToken(tk.id)"
          >
            <Trash2 class="size-4" aria-hidden="true" /><template v-if="confirmDelete === tk.id">{{ t('kernel.tokens.delete_confirm') }}</template>
          </JecButton>
        </li>
      </ul>
      <form id="token-form" class="flex flex-col gap-3 border-t border-line pt-3" novalidate @submit.prevent="submitToken">
        <h3 class="m-0 text-heading font-semibold">{{ t('kernel.tokens.new') }}</h3>
        <JecAlert v-if="tokenError.alert.value" :code="tokenError.alert.value.code" :params="tokenError.alert.value.params" />
        <JecField id="token-name" label="kernel.tokens.name" hint="kernel.tokens.name_help" :error="tokenError.fields.value.name">
          <JecInput id="token-name" v-model="tokenForm.name" maxlength="64" required :invalid="!!tokenError.fields.value.name" />
        </JecField>
        <JecField id="token-scope" label="kernel.tokens.scope">
          <select id="token-scope" v-model="tokenForm.scope" :class="control">
            <option value="read">{{ t('kernel.tokens.scope_read') }}</option>
            <option value="write">{{ t('kernel.tokens.scope_write') }}</option>
          </select>
        </JecField>
        <JecField id="token-password" label="kernel.account.confirm_password" :error="tokenError.fields.value.password">
          <JecInput
            id="token-password"
            v-model="tokenForm.password"
            type="password"
            autocomplete="current-password"
            required
            :invalid="!!tokenError.fields.value.password"
          />
        </JecField>
      </form>
    </div>
    <template #footer>
      <JecButton @click="tokensOpen = false">{{ t('kernel.action.close') }}</JecButton>
      <JecButton
        type="submit"
        form="token-form"
        variant="primary"
        :busy="createToken.isPending.value"
        :disabled="!tokenForm.name.trim() || !tokenForm.password"
      >
        {{ t('kernel.tokens.create') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="changing" :title="t('kernel.settings.change_password')" small>
    <form id="password-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitPassword">
      <JecAlert v-if="changeError.alert.value" :code="changeError.alert.value.code" :params="changeError.alert.value.params" />
      <JecField id="current-password" label="kernel.settings.current_password" :error="changeError.fields.value.password">
        <JecInput
          id="current-password"
          v-model="pw.current"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!changeError.fields.value.password"
        />
      </JecField>
      <JecField
        id="new-password"
        label="kernel.settings.new_password"
        hint="kernel.account.password_hint"
        :hint-params="{ min: 8 }"
        :error="changeError.fields.value.newPassword"
      >
        <JecInput
          id="new-password"
          v-model="pw.next"
          type="password"
          autocomplete="new-password"
          minlength="8"
          required
          :invalid="!!changeError.fields.value.newPassword"
        />
      </JecField>
      <JecField id="password-again" label="kernel.account.password_again" :error="mismatch ? 'kernel.account.password_mismatch' : null">
        <JecInput id="password-again" v-model="pw.again" type="password" autocomplete="new-password" required :invalid="mismatch" />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="changing = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton
        type="submit"
        form="password-form"
        variant="primary"
        :busy="change.isPending.value"
        :disabled="!pw.current || !pw.next || !pw.again"
      >
        {{ t('kernel.settings.change_password') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="turningOn" :title="t('kernel.two_factor.title_on')" small>
    <form id="two-factor-on-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitTurnOn">
      <JecAlert v-if="secretError.alert.value" :code="secretError.alert.value.code" :params="secretError.alert.value.params" />
      <JecAlert v-if="enableError.alert.value" :code="enableError.alert.value.code" :params="enableError.alert.value.params" />
      <JecField v-if="!secret.data.value" id="two-factor-password" label="kernel.account.confirm_password" :error="secretError.fields.value.password">
        <JecInput
          id="two-factor-password"
          v-model="tfPassword"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!secretError.fields.value.password"
        />
      </JecField>
      <template v-else>
        <p class="m-0">{{ t('kernel.two_factor.scan') }}</p>
        <img
          :src="secret.data.value.qr"
          :alt="t('kernel.two_factor.qr')"
          width="176"
          height="176"
          class="mx-auto block size-44 rounded-md border border-line bg-white"
        />
        <div class="min-w-0">
          <span class="block text-small font-semibold">{{ t('kernel.two_factor.key') }}</span>
          <code translate="no" class="block font-mono text-small select-all [overflow-wrap:anywhere]">{{ groupedKey }}</code>
        </div>
        <JecField id="two-factor-code" label="kernel.two_factor.code" :error="enableError.fields.value.code">
          <JecInput
            id="two-factor-code"
            v-model="tfCode"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="7"
            required
            :invalid="!!enableError.fields.value.code"
          />
        </JecField>
        <p class="m-0 text-small text-ink-muted">
          {{ t('kernel.two_factor.lost') }} <code class="whitespace-nowrap">{{ resetCommand }}</code>
        </p>
      </template>
    </form>
    <template #footer>
      <JecButton @click="turningOn = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton
        type="submit"
        form="two-factor-on-form"
        variant="primary"
        :busy="secret.isPending.value || enable.isPending.value"
        :disabled="secret.data.value ? !tfCode : !tfPassword"
      >
        {{ t(secret.data.value ? 'kernel.settings.two_factor_turn_on' : 'kernel.two_factor.next') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="turningOff" :title="t('kernel.two_factor.title_off')" small>
    <form id="two-factor-off-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitTurnOff">
      <p class="m-0">{{ t('kernel.two_factor.off_hint') }}</p>
      <JecAlert v-if="disableError.alert.value" :code="disableError.alert.value.code" :params="disableError.alert.value.params" />
      <JecField id="two-factor-off-password" label="kernel.account.confirm_password" :error="disableError.fields.value.password">
        <JecInput
          id="two-factor-off-password"
          v-model="tfPassword"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!disableError.fields.value.password"
        />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="turningOff = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="two-factor-off-form" variant="danger" :busy="disable.isPending.value" :disabled="!tfPassword">
        {{ t('kernel.settings.two_factor_turn_off') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
