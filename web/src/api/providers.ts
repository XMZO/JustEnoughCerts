import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, unwrap } from './client'
import type { components } from './schema'

export type ProviderTypeSummary = components['schemas']['TypeSummary']
export type ProviderType = components['schemas']['Type']
export type ProviderField = components['schemas']['Field']
export type Provider = components['schemas']['InstanceBody']
export type ProviderKind = ProviderTypeSummary['kind']

/** What a provider's form sends: plain settings, and the secrets typed in. */
export interface ProviderSettings {
  name: string
  config: Record<string, string>
  secrets: Record<string, string>
}

const all = ['providers'] as const

/** The provider types of kind that the modules that are on offer. */
export function useProviderTypes(kind: MaybeRefOrGetter<ProviderKind>) {
  return useQuery({
    queryKey: computed(() => [...all, 'types', toValue(kind)] as const),
    queryFn: async () => (await unwrap(api.GET('/providers/types', { params: { query: { kind: toValue(kind) } } }))).types ?? [],
    staleTime: 5 * 60_000,
  })
}

/** A provider type with the fields of its form, once id is known. */
export function useProviderType(id: MaybeRefOrGetter<string | undefined>) {
  return useQuery({
    queryKey: computed(() => [...all, 'type', toValue(id)] as const),
    queryFn: () => unwrap(api.GET('/providers/types/{id}', { params: { path: { id: toValue(id) ?? '' } } })),
    enabled: computed(() => !!toValue(id)),
    staleTime: 5 * 60_000,
  })
}

/** The configured providers of kind. */
export function useProviders(kind: MaybeRefOrGetter<ProviderKind>) {
  return useQuery({
    queryKey: computed(() => [...all, 'list', toValue(kind)] as const),
    queryFn: async () => (await unwrap(api.GET('/providers', { params: { query: { kind: toValue(kind) } } }))).providers ?? [],
  })
}

function useProvidersMutation<V, R>(fn: (vars: V) => Promise<R>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => client.invalidateQueries({ queryKey: [...all, 'list'] }),
  })
}

/** Tests a configured provider of a testable type; it fails with the provider's own error. */
export function useProviderTest() {
  return useMutation({
    mutationFn: (id: number) => unwrap(api.POST('/providers/{id}/test', { params: { path: { id } } })),
  })
}

export function useCreateProvider() {
  return useProvidersMutation((body: ProviderSettings & { type: string }) => unwrap(api.POST('/providers', { body })))
}

/** Changes provider id: the plain settings are replaced, the secrets given replace theirs. */
export function useUpdateProvider() {
  return useProvidersMutation(({ id, ...body }: ProviderSettings & { id: number }) =>
    unwrap(api.PATCH('/providers/{id}', { params: { path: { id } }, body })),
  )
}

export function useDeleteProvider() {
  return useProvidersMutation((id: number) => unwrap(api.DELETE('/providers/{id}', { params: { path: { id } } })))
}
