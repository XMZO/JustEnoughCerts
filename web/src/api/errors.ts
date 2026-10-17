import { computed, nextTick, type Ref } from 'vue'

import type { MessageKey } from '../i18n'
import { ApiError, networkError } from './client'

/**
 * useApiError turns the error of a query or mutation into an ApiError and
 * the fields it points at, by name without the "body." prefix. The alert
 * shows an error that names no field.
 */
export function useApiError(err: Ref<unknown>) {
  const error = computed(() => {
    if (!err.value) return null
    return err.value instanceof ApiError ? err.value : networkError
  })
  const fields = computed(() => {
    const out: Record<string, MessageKey> = {}
    for (const f of error.value?.fields ?? []) out[f.location.replace(/^body\./, '')] = f.code
    return out
  })
  const alert = computed(() => (error.value && !error.value.fields.length ? error.value : null))
  return { error, fields, alert }
}

/**
 * revealError scrolls the first error of a form that failed into view: the
 * alert or a field the server pointed at.
 */
export async function revealError(form: HTMLElement | null | undefined): Promise<void> {
  await nextTick()
  form?.querySelector('[role="alert"], [aria-invalid="true"]')?.scrollIntoView({ block: 'center', behavior: 'smooth' })
}
