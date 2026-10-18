import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { api, unwrap } from './client'
import type { components } from './schema'

export type CredentialKind = components['schemas']['CredentialKind']
export type Credential = components['schemas']['CredentialBody']

const all = ['credentials'] as const

/** The kinds of credential that the provider types of the modules that are on take. */
export function useCredentialKinds() {
  return useQuery({
    queryKey: [...all, 'kinds'] as const,
    queryFn: async () => (await unwrap(api.GET('/credentials/kinds'))).kinds ?? [],
    staleTime: 5 * 60_000,
  })
}

/** The credentials, with the providers that use each. */
export function useCredentials() {
  return useQuery({
    queryKey: [...all, 'list'] as const,
    queryFn: async () => (await unwrap(api.GET('/credentials'))).credentials ?? [],
  })
}

function useCredentialsMutation<V, R>(fn: (vars: V) => Promise<R>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => client.invalidateQueries({ queryKey: [...all, 'list'] }),
  })
}

/** What a credential's form sends: plain values, and the secrets typed in. */
export interface CredentialSettings {
  name: string
  config: Record<string, string>
  secrets: Record<string, string>
}

export function useCreateCredential() {
  return useCredentialsMutation((body: CredentialSettings & { kind: string }) => unwrap(api.POST('/credentials', { body })))
}

/** Changes credential id: the plain values are replaced, the secrets given replace theirs. */
export function useUpdateCredential() {
  return useCredentialsMutation(({ id, ...body }: CredentialSettings & { id: number }) =>
    unwrap(api.PATCH('/credentials/{id}', { params: { path: { id } }, body })),
  )
}

export function useDeleteCredential() {
  return useCredentialsMutation((id: number) => unwrap(api.DELETE('/credentials/{id}', { params: { path: { id } } })))
}

/**
 * hint is the value that tells a credential apart, shown in part, as an
 * AccessKey ID: its first and last four characters, or its first two of a
 * short one.
 */
export function hint(kind: CredentialKind | undefined, c: Credential): string {
  const v = kind?.hint ? (c.config[kind.hint] ?? '') : ''
  if (!v) return ''
  return v.length > 10 ? `${v.slice(0, 4)}••••${v.slice(-4)}` : `${v.slice(0, 2)}••••`
}
