<script setup lang="ts">
import type { MessageKey, Params } from '../i18n'
import JecField from './JecField.vue'
import JecTextarea from './JecTextarea.vue'

// The two PEM texts of an upload, with the errors the server found in them
// by field name ("certificate", "privateKey").
const certificate = defineModel<string>('certificate', { required: true })
const privateKey = defineModel<string>('privateKey', { required: true })
defineProps<{ errors: Record<string, MessageKey>; errorParams?: Params }>()

const certPlaceholder = '-----BEGIN CERTIFICATE-----'
const keyPlaceholder = '-----BEGIN PRIVATE KEY-----'
</script>

<template>
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
