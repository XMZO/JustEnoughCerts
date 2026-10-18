import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { addMessages } from '../i18n'
import { api, unwrap } from './client'
import type { components } from './schema'

export type ProviderTypeSummary = components['schemas']['TypeSummary']
export type ProviderType = components['schemas']['Type']
export type ProviderField = components['schemas']['Field']
export type Provider = components['schemas']['InstanceBody']
export type ProviderKind = ProviderTypeSummary['kind']

/**
 * What a provider's form sends: plain settings, and the secrets typed in;
 * for a type that takes a credential, the credential that fills its fields
 * (0 for none, on an edit), or that the values of those fields become a new
 * one.
 */
export interface ProviderSettings {
  name: string
  config: Record<string, string>
  secrets: Record<string, string>
  credentialId?: number
  newCredential?: boolean
}

const all = ['providers'] as const

/** withTexts adds the messages a type brings along, as a plugin's do, before its texts show. */
function withTexts<T extends { texts?: Record<string, Record<string, string>> }>(type: T): T {
  addMessages(type.texts)
  return type
}

/** The provider types of kind that the modules that are on offer. */
export function useProviderTypes(kind: MaybeRefOrGetter<ProviderKind>) {
  return useQuery({
    queryKey: computed(() => [...all, 'types', toValue(kind)] as const),
    queryFn: async () =>
      ((await unwrap(api.GET('/providers/types', { params: { query: { kind: toValue(kind) } } }))).types ?? []).map(withTexts),
    staleTime: 5 * 60_000,
  })
}

/** A provider type with the fields of its form, once id is known. */
export function useProviderType(id: MaybeRefOrGetter<string | undefined>) {
  return useQuery({
    queryKey: computed(() => [...all, 'type', toValue(id)] as const),
    queryFn: async () => withTexts(await unwrap(api.GET('/providers/types/{id}', { params: { path: { id: toValue(id) ?? '' } } }))),
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
    // The credentials tell which providers use them, and an instance may
    // keep its keys as a new one.
    onSuccess: () => Promise.all([client.invalidateQueries({ queryKey: [...all, 'list'] }), client.invalidateQueries({ queryKey: ['credentials'] })]),
  })
}

/**
 * Tests a configured provider of a testable type; it fails with the
 * provider's own error. The provider keeps what the test said.
 */
export function useProviderTest() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.POST('/providers/{id}/test', { params: { path: { id } } })),
    onSettled: () => client.invalidateQueries({ queryKey: [...all, 'list'] }),
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
