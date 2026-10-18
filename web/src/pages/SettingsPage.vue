<script setup lang="ts">
import { Check, Download, KeyRound } from '@lucide/vue'
import { computed, ref, watch, watchEffect } from 'vue'

import { useChangePassword, useSession } from '../api/account'
import { saveFile, useCertSettings, useSaveCertSettings, type CertSettingsInput, type KeyType } from '../api/certs'
import { useApiError } from '../api/errors'
import { useManifest } from '../api/manifest'
import { useBackup, useSystem } from '../api/system'
import { keyText } from '../certs/life'
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
</template>
