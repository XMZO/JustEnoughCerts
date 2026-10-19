import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, unwrap } from './client'
import type { components } from './schema'

export type Site = components['schemas']['SiteBody']

const key = ['watch', 'sites'] as const

/** The sites watched, with what each served when last checked; only while enabled, as while their module is on. */
export function useSites(enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: key,
    queryFn: async () => (await unwrap(api.GET('/watch/sites'))).sites,
    refetchInterval: 60_000,
    enabled: computed(() => toValue(enabled)),
  })
}

/** needsLook tells a site whose certificate someone should look at: ending, ended, not trusted or not seen. */
export const needsLook = (s: Site) => s.state === 'expiring' || s.state === 'expired' || s.state === 'invalid' || s.state === 'unreachable'

/** Watches a site, which the server checks before it answers. */
export function useAddSite() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { host: string; name?: string }) => unwrap(api.POST('/watch/sites', { body })),
    onSuccess: () => client.invalidateQueries({ queryKey: key }),
  })
}

/** Checks a site now. */
export function useCheckSite() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.POST('/watch/sites/{id}/check', { params: { path: { id } } })),
    onSuccess: () => client.invalidateQueries({ queryKey: key }),
  })
}

/** Stops watching a site. */
export function useDeleteSite() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.DELETE('/watch/sites/{id}', { params: { path: { id } } })),
    onSuccess: () => client.invalidateQueries({ queryKey: key }),
  })
}
