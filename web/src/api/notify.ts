import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { toValue, type MaybeRefOrGetter } from 'vue'

import { api, unwrap } from './client'
import type { components } from './schema'

export type Send = components['schemas']['SendBody']

const all = ['notify'] as const

/** The last note sent through each notifier. */
export function useSends(enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: [...all, 'sends'] as const,
    queryFn: async () => (await unwrap(api.GET('/notify/sends'))).sends ?? [],
    enabled: () => toValue(enabled),
  })
}

/** Sends a test note through a notifier; it fails with the notifier's own error. */
export function useSendTest() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.POST('/notify/notifiers/{id}/test', { params: { path: { id } } })),
    onSettled: () => client.invalidateQueries({ queryKey: [...all, 'sends'] }),
  })
}
