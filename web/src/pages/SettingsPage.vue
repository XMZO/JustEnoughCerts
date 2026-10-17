<script setup lang="ts">
import { Check } from '@lucide/vue'
import { computed, ref, watchEffect } from 'vue'

import { useCertSettings, useSaveCertSettings, type CertSettingsInput, type KeyType } from '../api/certs'
import { useApiError } from '../api/errors'
import { useManifest } from '../api/manifest'
import { keyText } from '../certs/life'
import JecAlert from '../components/JecAlert.vue'
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
</template>
