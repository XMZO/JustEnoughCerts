<script setup lang="ts">
import { CircleCheck, CircleX, FlaskConical, TriangleAlert } from '@lucide/vue'
import { computed, ref, watch } from 'vue'

import { useApiError } from '../api/errors'
import { useTryProvider, type TryResult } from '../api/providers'
import { useT, type MessageKey } from '../i18n'
import JecAlert from './JecAlert.vue'
import JecButton from './JecButton.vue'
import JecDialog from './JecDialog.vue'
import JecField from './JecField.vue'
import JecInput from './JecInput.vue'

// A trial of a solver: on a domain, it adds the TXT record of a challenge
// and waits for the authoritative servers to answer it, or, for a solver
// of files on the website, puts a file and fetches it from the domain as a
// CA would; then removes it, as an issuance would, without a CA.
const props = defineProps<{ id: number; name: string; suggested: string; http?: boolean }>()
const word = (dns: MessageKey, http: MessageKey) => t(props.http ? http : dns)
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
      <p class="m-0">{{ word('kernel.providers.try_intro', 'kernel.providers.try_http_intro') }}</p>
      <JecField id="try-domain" label="kernel.providers.try_domain" :error="error.fields.value.domain">
        <JecInput id="try-domain" v-model="domain" autocapitalize="none" spellcheck="false" required :invalid="!!error.fields.value.domain" />
      </JecField>
      <JecAlert v-if="error.alert.value" :code="error.alert.value.code" :params="error.alert.value.params" />
      <ul v-if="result" class="m-0 flex list-none flex-col gap-1.5 p-0 text-small" role="status">
        <li class="flex items-start gap-2">
          <CircleCheck class="mt-0.5 size-4 flex-none text-ok-ink" aria-hidden="true" />
          <span class="min-w-0">
            {{ word('kernel.providers.try_added', 'kernel.providers.try_http_put') }}
            <code class="block [overflow-wrap:anywhere]" translate="no">{{ result.record }}</code>
            <template v-if="result.via">
              {{ t('kernel.providers.try_via') }}
              <code class="block [overflow-wrap:anywhere]" translate="no">{{ result.via }}</code>
            </template>
          </span>
        </li>
        <li class="flex items-start gap-2">
          <component :is="result.visible ? CircleCheck : TriangleAlert" :class="['mt-0.5 size-4 flex-none', result.visible ? 'text-ok-ink' : 'text-warn-ink']" aria-hidden="true" />
          <span v-if="props.http" class="min-w-0 [overflow-wrap:anywhere]">
            {{
              result.visible
                ? t('kernel.providers.try_http_visible', { seconds: result.seconds })
                : t('kernel.providers.try_http_not_visible', { seconds: result.seconds })
            }}
            <code v-if="!result.visible && result.answer" class="block" translate="no">{{ result.answer }}</code>
          </span>
          <span v-else>{{ result.visible ? t('kernel.providers.try_visible', { seconds: result.seconds }) : t('kernel.providers.try_not_visible', { seconds: result.seconds }) }}</span>
        </li>
        <li class="flex items-start gap-2">
          <component :is="result.removed ? CircleCheck : CircleX" :class="['mt-0.5 size-4 flex-none', result.removed ? 'text-ok-ink' : 'text-danger-ink']" aria-hidden="true" />
          <span>{{ result.removed ? word('kernel.providers.try_removed', 'kernel.providers.try_http_removed') : word('kernel.providers.try_not_removed', 'kernel.providers.try_http_not_removed') }}</span>
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
