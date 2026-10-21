import { keepPreviousData, useQuery } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, unwrap } from './client'
import type { components, operations } from './schema'

export type AuditEntry = components['schemas']['EntryBody']
export type AuditQuery = NonNullable<operations['audit-entries']['parameters']['query']>

/**
 * A page of what was done through the API, the newest first, as q asks:
 * the last days (0 for all), the result, the text, the page and its size,
 * as many as fit the window (fit.ts): nothing is asked until that is
 * known. The page before stays while the next one loads.
 */
export function useAuditEntries(q: MaybeRefOrGetter<AuditQuery>) {
  return useQuery({
    queryKey: computed(() => ['audit', 'entries', toValue(q)] as const),
    queryFn: () => unwrap(api.GET('/audit/entries', { params: { query: toValue(q) } })),
    placeholderData: keepPreviousData,
    enabled: computed(() => (toValue(q).size ?? 0) > 0),
  })
}

/** Every entry q asks for, at most 5000, for an export. */
export async function auditExport(q: AuditQuery): Promise<AuditEntry[]> {
  return (await unwrap(api.GET('/audit/entries', { params: { query: { ...q, page: 1, size: 5000 } } }))).entries
}
