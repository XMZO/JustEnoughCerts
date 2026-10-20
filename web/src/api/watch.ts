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

export type Transparency = components['schemas']['TransparencyBody']
export type Spotted = components['schemas']['SpottedBody']
export type TransparencyTrust = components['schemas']['TransparencyTrustBody']

const ctKey = ['watch', 'transparency'] as const

/** The search of certificate transparency logs and what it found; only while enabled, as while the module is on. */
export function useTransparency(enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: ctKey,
    queryFn: () => unwrap(api.GET('/watch/transparency')),
    refetchInterval: 5 * 60_000,
    enabled: computed(() => toValue(enabled)),
  })
}

/** spottedNeedsLook tells a certificate found that was told of: someone should see who asked for it. */
export const spottedNeedsLook = (s: Spotted) => s.state === 'unknown'

/** Switches the search, and sets its API key: left out, the saved one stays; empty, it goes. */
export function useSaveTransparency() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { enabled: boolean; apiKey?: string }) => unwrap(api.PUT('/watch/transparency', { body })),
    onSuccess: (data) => client.setQueryData(ctKey, data),
  })
}

/** Has every domain searched soon, in the background. */
export function useSearchTransparency() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(api.POST('/watch/transparency/check')),
    onSuccess: () => client.invalidateQueries({ queryKey: ctKey }),
  })
}

/** Trusts the certificates for the names of one found, by its issuer, or ignores that one. */
export function useJudgeSpotted() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, how }: { id: string; how: 'trust' | 'ignore' }) =>
      how === 'trust'
        ? unwrap(api.POST('/watch/transparency/certificates/{id}/trust', { params: { path: { id } } }))
        : unwrap(api.POST('/watch/transparency/certificates/{id}/ignore', { params: { path: { id } } })),
    onSuccess: () => client.invalidateQueries({ queryKey: ctKey }),
  })
}

/** Stops trusting the certificates for some names by an issuer. */
export function useUntrust() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { names: string[]; issuer: string }) => unwrap(api.POST('/watch/transparency/untrust', { body })),
    onSuccess: () => client.invalidateQueries({ queryKey: ctKey }),
  })
}
