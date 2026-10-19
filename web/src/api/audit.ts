import { keepPreviousData, useQuery } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, unwrap } from './client'
import type { components, operations } from './schema'

export type AuditEntry = components['schemas']['EntryBody']
export type AuditQuery = NonNullable<operations['audit-entries']['parameters']['query']>

/** pageSize is how many entries a page of the log holds. */
export const pageSize = 50

/**
 * A page of what was done through the API, the newest first, as q asks:
 * the last days (0 for all), the result, the text. The page before stays
 * while the next one loads.
 */
export function useAuditEntries(q: MaybeRefOrGetter<AuditQuery>) {
  return useQuery({
    queryKey: computed(() => ['audit', 'entries', toValue(q)] as const),
    queryFn: () => unwrap(api.GET('/audit/entries', { params: { query: { size: pageSize, ...toValue(q) } } })),
    placeholderData: keepPreviousData,
  })
}

/** Every entry q asks for, at most 5000, for an export. */
export async function auditExport(q: AuditQuery): Promise<AuditEntry[]> {
  return (await unwrap(api.GET('/audit/entries', { params: { query: { ...q, page: 1, size: 5000 } } }))).entries
}
