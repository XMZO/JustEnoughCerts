import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, unwrap } from './client'
import { useManifest } from './manifest'
import type { components } from './schema'

export type Site = components['schemas']['SiteBody']

const key = ['watch', 'sites'] as const

/** useWatching tells whether the watch module, of the sites and domains, is on. */
export function useWatching() {
  const manifest = useManifest()
  return computed(() => manifest.data.value?.modules.some((m) => m.id === 'watch' && m.enabled) ?? false)
}

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
    onSuccess: () => client.invalidateQueries({ queryKey: ['watch'] }),
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
    onSuccess: () => client.invalidateQueries({ queryKey: ['watch'] }),
  })
}

export type Domain = components['schemas']['DomainBody']

const domainsKey = ['watch', 'domains'] as const

/** The domains whose registration is watched; only while enabled, as while their module is on. */
export function useDomains(enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: domainsKey,
    queryFn: async () => (await unwrap(api.GET('/watch/domains'))).domains,
    refetchInterval: 5 * 60_000,
    enabled: computed(() => toValue(enabled)),
  })
}

/** domainNeedsLook tells a domain whose registration ends within 30 days or ended. */
export const domainNeedsLook = (d: Domain) => d.state === 'expiring' || d.state === 'expired'

/** Watches a domain by hand, which the server looks up before it answers. */
export function useAddDomain() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => unwrap(api.POST('/watch/domains', { body: { name } })),
    onSuccess: () => client.invalidateQueries({ queryKey: domainsKey }),
  })
}

/** Looks a domain up now. */
export function useCheckDomain() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => unwrap(api.POST('/watch/domains/{name}/check', { params: { path: { name } } })),
    onSuccess: () => client.invalidateQueries({ queryKey: domainsKey }),
  })
}

/** Stops watching a domain added by hand. */
export function useDeleteDomain() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => unwrap(api.DELETE('/watch/domains/{name}', { params: { path: { name } } })),
    onSuccess: () => client.invalidateQueries({ queryKey: domainsKey }),
  })
}
