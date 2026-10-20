<script setup lang="ts">
import { CircleCheck, CircleX, FlaskConical, TriangleAlert } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { useApiError } from '../api/errors'
import { useTryProvider, type TryResult } from '../api/providers'
import { useT } from '../i18n'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'

// A trial of a solver of DNS: on a domain, it adds the TXT record of a
// challenge, waits for the authoritative servers to answer it, and removes
// it, as an issuance would, without a CA.
const props = defineProps<{ id: number; name: string; suggested: string }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useT()
const domain = ref('')
const result = ref<TryResult>()
const run = useTryProvider()
const error = useApiError(computed(() => run.error.value))
watch(open, (now) => {
  if (!now) return
  domain.value = props.suggested
  result.value = undefined
  run.reset()
})
async function submit() {
  result.value = undefined
  try {
    result.value = await run.mutateAsync({ id: props.id, domain: domain.value.trim() })
  } catch {
    // shown by the alert
  }
}
</script>

<template>
  <JecDialog v-model:open="open" :title="t('kernel.providers.try_title', { name })" small>
    <form id="try-form" class="flex flex-col gap-3" novalidate @submit.prevent="submit">
      <p class="m-0">{{ t('kernel.providers.try_intro') }}</p>
      <JecField id="try-domain" label="kernel.providers.try_domain" :error="error.fields.value.domain">
        <JecInput id="try-domain" v-model="domain" autocapitalize="none" spellcheck="false" required :invalid="!!error.fields.value.domain" />
      </JecField>
      <JecAlert v-if="error.alert.value" :code="error.alert.value.code" :params="error.alert.value.params" />
      <ul v-if="result" class="m-0 flex list-none flex-col gap-1.5 p-0 text-small" role="status">
        <li class="flex items-start gap-2">
          <CircleCheck class="mt-0.5 size-4 flex-none text-ok-ink" aria-hidden="true" />
          <span class="min-w-0">
            {{ t('kernel.providers.try_added') }}
            <code class="block [overflow-wrap:anywhere]" translate="no">{{ result.record }}</code>
          </span>
        </li>
        <li class="flex items-start gap-2">
          <component :is="result.visible ? CircleCheck : TriangleAlert" :class="['mt-0.5 size-4 flex-none', result.visible ? 'text-ok-ink' : 'text-warn-ink']" aria-hidden="true" />
          <span>{{ result.visible ? t('kernel.providers.try_visible', { seconds: result.seconds }) : t('kernel.providers.try_not_visible', { seconds: result.seconds }) }}</span>
        </li>
        <li class="flex items-start gap-2">
          <component :is="result.removed ? CircleCheck : CircleX" :class="['mt-0.5 size-4 flex-none', result.removed ? 'text-ok-ink' : 'text-danger-ink']" aria-hidden="true" />
          <span>{{ t(result.removed ? 'kernel.providers.try_removed' : 'kernel.providers.try_not_removed') }}</span>
        </li>
      </ul>
    </form>
    <template #footer>
      <JecButton @click="open = false">{{ t('kernel.action.close') }}</JecButton>
      <JecButton type="submit" form="try-form" variant="primary" :busy="run.isPending.value" :disabled="!domain.trim()">
        <FlaskConical class="size-4" aria-hidden="true" />{{ t('kernel.providers.try') }}
      </JecButton>
    </template>
  </JecDialog>
</template>
