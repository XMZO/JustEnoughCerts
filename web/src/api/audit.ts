import { useQuery } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, unwrap } from './client'
import type { components } from './schema'

export type AuditEntry = components['schemas']['EntryBody']

/** What was done through the API in the last days (0 for all), the newest first. */
export function useAuditEntries(days: MaybeRefOrGetter<number>) {
  return useQuery({
    queryKey: computed(() => ['audit', 'entries', toValue(days)] as const),
    queryFn: () => unwrap(api.GET('/audit/entries', { params: { query: { days: toValue(days) } } })),
  })
}
