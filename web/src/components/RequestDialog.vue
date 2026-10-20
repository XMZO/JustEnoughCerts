<script setup lang="ts">
import { Download, FilePlus } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { saveFile, useNewRequest, type KeyType } from '../api/certs'
import { useApiError } from '../api/errors'
import { keyText } from '../certs/life'
import { useT } from '../i18n'
import { useIntl } from '../i18n/intl'
import CopyButton from './CopyButton.vue'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'

// The CSR of a certificate bought from a CA by hand: the one it waits on,
// to hand to the CA, or a new one, with a key JEC makes and keeps. The
// certificate the CA issues for it is uploaded without a key.
export interface Request {
  csr: string
  keyType: string
  createdAt: string
}
const props = defineProps<{ id: number; domain: string; request?: Request | null; keyType: string }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useT()
const intl = useIntl()
const make = useNewRequest(() => props.id)
const error = useApiError(computed(() => make.error.value))

const keyTypes: KeyType[] = ['rsa-2048', 'ecdsa-p256', 'ecdsa-p384', 'rsa-3072', 'rsa-4096']
const chosen = ref<KeyType>('rsa-2048')
// A new one asked for while one is shown.
const anew = ref(false)
watch(open, (now) => {
  if (!now) return
  make.reset()
  anew.value = false
  chosen.value = keyTypes.includes(props.keyType as KeyType) ? (props.keyType as KeyType) : 'rsa-2048'
})
const shown = computed(() => (anew.value ? null : (props.request ?? null)))
const keyLabel = (k: string) => {
  const text = keyText(k)
  return t(text.key, text.params)
}

async function generate() {
  try {
    await make.mutateAsync(chosen.value)
    anew.value = false
  } catch {
    // shown by the alert
  }
}
function download() {
  if (shown.value) saveFile(new Blob([shown.value.csr], { type: 'application/pkcs10' }), `${props.domain.replace('*', 'star')}.csr`)
}
</script>

<template>
  <JecDialog v-model:open="open" :title="t('certs.request.title', { domain })">
    <div class="flex flex-col gap-3">
      <JecAlert v-if="error.alert.value" :code="error.alert.value.code" :params="error.alert.value.params" />
      <template v-if="shown">
        <p class="m-0 text-small text-ink-muted">{{ t('certs.request.shown') }}</p>
        <p class="m-0 text-small">
          {{ t('certs.request.made', { key: keyLabel(shown.keyType) }) }}
          <time :datetime="shown.createdAt">{{ intl.dateTime(shown.createdAt) }}</time>
        </p>
        <div class="relative">
          <pre
            translate="no"
            tabindex="0"
            :aria-label="t('certs.field.csr')"
            class="m-0 max-h-64 overflow-auto rounded-md bg-surface-sunken p-3 pr-10 font-mono text-caption whitespace-pre"
          >{{ shown.csr }}</pre>
          <span class="absolute top-1.5 right-1.5"><CopyButton :value="shown.csr" label="certs.csr.copy" /></span>
        </div>
        <button
          type="button"
          class="cursor-pointer self-start border-0 bg-transparent p-0 text-small text-brand underline underline-offset-2 hover:text-brand-hover"
          @click="anew = true"
        >
          {{ t('certs.request.again') }}
        </button>
      </template>
      <template v-else>
        <p class="m-0 text-small text-ink-muted">{{ t('certs.request.intro') }}</p>
        <JecField id="request-key" label="certs.add.key_type" hint="certs.request.key_help">
          <select
            id="request-key"
            v-model="chosen"
            class="h-9 w-full rounded-md border border-line-strong bg-surface-raised px-2 text-ink max-sm:h-11 sm:max-w-60"
          >
            <option v-for="k in keyTypes" :key="k" :value="k">{{ keyLabel(k) }}</option>
          </select>
        </JecField>
      </template>
    </div>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.close') }}</JecButton>
      <JecButton v-if="shown" variant="primary" @click="download">
        <Download class="size-4" aria-hidden="true" />{{ t('certs.csr.download') }}
      </JecButton>
      <JecButton v-else variant="primary" :busy="make.isPending.value" @click="generate">
        <FilePlus class="size-4" aria-hidden="true" />{{ t('certs.request.generate') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
