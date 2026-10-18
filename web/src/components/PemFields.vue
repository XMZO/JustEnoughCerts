<script setup lang="ts">
import { ref, watch } from 'vue'

import { useT, type MessageKey, type Params } from '../i18n'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'
import JecTextarea from './JecTextarea.vue'

// The certificate of an upload: the two PEM texts, or a PKCS #12 file
// (.pfx, .p12) with its password, as Windows, IIS and many panels export
// one. The errors the server found come by field name ("certificate",
// "privateKey", "pfx", "pfxPassword").
const certificate = defineModel<string>('certificate', { required: true })
const privateKey = defineModel<string>('privateKey', { required: true })
/** The file, in Base64. */
const pfx = defineModel<string>('pfx', { required: true })
const pfxPassword = defineModel<string>('pfxPassword', { required: true })
defineProps<{ errors: Record<string, MessageKey>; errorParams?: Params }>()

const { t } = useT()

const certPlaceholder = '-----BEGIN CERTIFICATE-----'
const keyPlaceholder = '-----BEGIN PRIVATE KEY-----'

type Format = 'pem' | 'pfx'
const formats: { id: Format; label: MessageKey }[] = [
  { id: 'pem', label: 'certs.add.format_pem' },
  { id: 'pfx', label: 'certs.add.format_pfx' },
]
const format = ref<Format>('pem')
// Only one of the two goes to the server.
watch(format, (f) => {
  if (f === 'pem') {
    pfx.value = ''
    pfxPassword.value = ''
  } else {
    certificate.value = ''
    privateKey.value = ''
  }
})

async function pick(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  pfx.value = file ? await base64(file) : ''
}

async function base64(file: Blob): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer())
  let text = ''
  for (let i = 0; i < bytes.length; i += 0x8000) text += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  return btoa(text)
}
</script>

<template>
  <div role="group" :aria-label="t('certs.add.format')" class="flex flex-wrap gap-2 sm:gap-1.5">
    <button
      v-for="f in formats"
      :key="f.id"
      type="button"
      :aria-pressed="format === f.id ? 'true' : 'false'"
      :class="[
        'inline-flex cursor-pointer items-center border font-semibold text-ink',
        'h-9 rounded-full px-3.5 text-small sm:h-[30px] sm:rounded-md sm:px-3',
        format === f.id ? 'border-brand bg-brand-soft' : 'border-line-strong bg-surface-raised hover:bg-surface-sunken',
      ]"
      @click="format = f.id"
    >
      {{ t(f.label) }}
    </button>
  </div>
  <template v-if="format === 'pem'">
    <JecField id="certificate" label="certs.add.certificate" hint="certs.add.certificate_help" :error="errors.certificate" :error-params="errorParams">
      <JecTextarea
        id="certificate"
        v-model="certificate"
        :rows="6"
        :invalid="!!errors.certificate"
        :placeholder="certPlaceholder"
        translate="no"
      />
    </JecField>
    <JecField id="privateKey" label="certs.add.key" hint="certs.add.key_help" :error="errors.privateKey" :error-params="errorParams">
      <JecTextarea id="privateKey" v-model="privateKey" :rows="4" :invalid="!!errors.privateKey" :placeholder="keyPlaceholder" translate="no" />
    </JecField>
  </template>
  <template v-else>
    <JecField id="pfx" label="certs.add.pfx_file" hint="certs.add.pfx_file_help" :error="errors.pfx" :error-params="errorParams">
      <input
        id="pfx"
        type="file"
        accept=".pfx,.p12,application/x-pkcs12"
        :aria-invalid="!!errors.pfx || undefined"
        class="block w-full min-w-0 text-small text-ink file:mr-3 file:h-9 file:cursor-pointer file:rounded-md file:border file:border-solid file:border-line-strong file:bg-surface-raised file:px-3 file:font-semibold file:text-ink hover:file:bg-surface-sunken"
        @change="pick"
      />
    </JecField>
    <JecField id="pfxPassword" label="certs.add.pfx_password" hint="certs.add.pfx_password_help" :error="errors.pfxPassword">
      <JecInput id="pfxPassword" v-model="pfxPassword" type="password" autocomplete="off" :invalid="!!errors.pfxPassword" />
    </JecField>
  </template>
</template>
