<script setup lang="ts">
import { Check, CloudUpload, Settings2, TriangleAlert } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { useApiError } from '../api/errors'
import { useOffsiteSettings, useSaveOffsite } from '../api/offsite'
import { useT } from '../i18n'
import { useIntl } from '../i18n/intl'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'

// Copies of the daily backups in an S3 compatible bucket (module offsite):
// a row of the data settings, with how the last copy went, and the dialog
// that sets the bucket up.
defineProps<{ rowClass: string }>()

const { t } = useT()
const intl = useIntl()
const settings = useOffsiteSettings()
const save = useSaveOffsite()
const errors = useApiError(computed(() => save.error.value))
const open = ref(false)

const form = ref({ enabled: true, endpoint: '', region: '', bucket: '', prefix: '', pathStyle: false, accessKey: '', secretKey: '' })
watch(open, (now) => {
  const s = settings.data.value
  if (!now || !s) return
  save.reset()
  form.value = {
    enabled: s.enabled || !s.endpoint, // a first setup means to switch it on
    endpoint: s.endpoint,
    region: s.region,
    bucket: s.bucket,
    prefix: s.prefix,
    pathStyle: s.pathStyle,
    accessKey: s.accessKey,
    secretKey: '',
  }
})

async function submit() {
  const { secretKey, ...rest } = form.value
  try {
    // Left empty, the key saved stays.
    await save.mutateAsync({ ...rest, ...(secretKey ? { secretKey } : {}) })
  } catch {
    return // shown by the fields and the alert
  }
  open.value = false
}
</script>

<template>
  <div :class="[rowClass, 'flex-wrap']">
    <div class="min-w-0 flex-[1_1_12rem]">
      <span class="block font-semibold">{{ t('offsite.settings.title') }}</span>
      <span class="block text-caption leading-[18px] text-ink-muted">
        <template v-if="settings.data.value?.enabled && settings.data.value.backupsOff">{{ t('offsite.settings.backups_off') }}</template>
        <template v-else-if="settings.data.value?.enabled && settings.data.value.lastError">
          <span class="font-semibold text-warn-ink">{{ t('offsite.settings.failed') }}</span>
          <span translate="no">{{ settings.data.value.lastError }}</span>
        </template>
        <template v-else-if="settings.data.value?.enabled && settings.data.value.lastCopy">
          {{ t('offsite.settings.copied', { count: settings.data.value.copied }) }}
          <time :datetime="settings.data.value.lastCopy">{{ intl.dateTime(settings.data.value.lastCopy) }}</time>
        </template>
        <template v-else>{{ t('offsite.settings.hint') }}</template>
      </span>
    </div>
    <span v-if="settings.data.value?.enabled && settings.data.value.lastError" class="inline-flex flex-none items-center gap-1 text-small font-semibold text-warn-ink">
      <TriangleAlert class="size-4" aria-hidden="true" />{{ t('offsite.settings.state_failed') }}
    </span>
    <span v-else-if="settings.data.value?.enabled" class="inline-flex flex-none items-center gap-1 text-small font-semibold text-ok-ink">
      <Check class="size-4" aria-hidden="true" />{{ t('offsite.settings.state_on') }}
    </span>
    <span v-else-if="settings.data.value" class="inline-flex flex-none items-center gap-1 text-small text-ink-muted">
      {{ t('offsite.settings.state_off') }}
    </span>
    <JecButton size="sm" class="ml-auto flex-none" :disabled="!settings.data.value" @click="open = true">
      <Settings2 class="size-4" aria-hidden="true" />{{ t('offsite.settings.edit') }}
    </JecButton>
  </div>

  <JecDialog v-model:open="open" :title="t('offsite.settings.title')">
    <form id="offsite-form" class="flex flex-col gap-3" novalidate @submit.prevent="submit">
      <p class="m-0">{{ t('offsite.settings.intro') }}</p>
      <JecAlert v-if="errors.alert.value" :code="errors.alert.value.code" :params="errors.alert.value.params" />
      <label class="flex min-h-9 items-center gap-2.5 font-semibold max-sm:min-h-11">
        <input v-model="form.enabled" type="checkbox" class="size-4 flex-none accent-brand" />{{ t('offsite.settings.enabled') }}
      </label>
      <JecField id="offsite-endpoint" label="offsite.settings.endpoint" hint="offsite.settings.endpoint_hint" :error="errors.fields.value.endpoint">
        <JecInput
          id="offsite-endpoint"
          v-model="form.endpoint"
          type="url"
          inputmode="url"
          autocapitalize="none"
          spellcheck="false"
          :invalid="!!errors.fields.value.endpoint"
        />
      </JecField>
      <div class="grid gap-3 sm:grid-cols-2">
        <JecField id="offsite-bucket" label="offsite.settings.bucket" :error="errors.fields.value.bucket">
          <JecInput id="offsite-bucket" v-model="form.bucket" autocapitalize="none" spellcheck="false" :invalid="!!errors.fields.value.bucket" />
        </JecField>
        <JecField id="offsite-region" label="offsite.settings.region" hint="offsite.settings.region_hint">
          <JecInput id="offsite-region" v-model="form.region" autocapitalize="none" spellcheck="false" />
        </JecField>
      </div>
      <JecField id="offsite-prefix" label="offsite.settings.prefix" hint="offsite.settings.prefix_hint">
        <JecInput id="offsite-prefix" v-model="form.prefix" autocapitalize="none" spellcheck="false" />
      </JecField>
      <label class="flex cursor-pointer items-start gap-2">
        <input v-model="form.pathStyle" type="checkbox" class="mt-1 size-4 flex-none accent-brand" />
        <span class="min-w-0">
          <span class="block font-semibold">{{ t('offsite.settings.path_style') }}</span>
          <span class="block text-small text-ink-muted">{{ t('offsite.settings.path_style_hint') }}</span>
        </span>
      </label>
      <JecField id="offsite-access" label="offsite.settings.access_key" :error="errors.fields.value.accessKey">
        <JecInput id="offsite-access" v-model="form.accessKey" autocapitalize="none" spellcheck="false" :invalid="!!errors.fields.value.accessKey" />
      </JecField>
      <JecField
        id="offsite-secret"
        label="offsite.settings.secret_key"
        :hint="settings.data.value?.hasSecret ? 'offsite.settings.secret_kept' : undefined"
        :error="errors.fields.value.secretKey"
      >
        <JecInput id="offsite-secret" v-model="form.secretKey" type="password" autocomplete="off" :invalid="!!errors.fields.value.secretKey" />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="offsite-form" variant="primary" :busy="save.isPending.value">
        <CloudUpload class="size-4" aria-hidden="true" />{{ t('kernel.action.save') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
