import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { api, unwrap } from './client'
import type { components } from './schema'

export type Site = components['schemas']['SiteBody']

const key = ['watch', 'sites'] as const

/** The sites watched, with what each served when last checked. */
export function useSites() {
  return useQuery({ queryKey: key, queryFn: async () => (await unwrap(api.GET('/watch/sites'))).sites, refetchInterval: 60_000 })
}

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
