import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, ApiError, unwrap } from './client'
import { queryClient } from './query'
import type { components } from './schema'

export const sessionKey = ['session'] as const
export const setupKey = ['setup'] as const

export type Account = Awaited<ReturnType<typeof fetchSession>>
export type ApiToken = components['schemas']['TokenBody']

/** The signed-in account, or null when nobody is signed in. */
async function fetchSession() {
  try {
    return await unwrap(api.GET('/session'))
  } catch (err) {
    if (err instanceof ApiError && err.status === 401) return null
    throw err
  }
}

function fetchSetupStatus() {
  return unwrap(api.GET('/setup'))
}

export const sessionQuery = { queryKey: sessionKey, queryFn: fetchSession, staleTime: Infinity }
export const setupQuery = { queryKey: setupKey, queryFn: fetchSetupStatus, staleTime: Infinity }

/** where tells which page fits the visitor: setup, sign-in or the app. */
export async function where(): Promise<'setup' | 'login' | 'app'> {
  if (await queryClient.fetchQuery(sessionQuery)) return 'app'
  const setup = await queryClient.fetchQuery(setupQuery)
  return setup.required ? 'setup' : 'login'
}

export function useSession() {
  return useQuery(sessionQuery)
}

export function useSignIn() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { username: string; password: string; code?: string }) => unwrap(api.POST('/session', { body })),
    onSuccess: (account) => client.setQueryData(sessionKey, account),
  })
}

export function useSetup() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { token: string; username: string; password: string; locale?: string }) =>
      unwrap(api.POST('/setup', { body })),
    onSuccess: (account) => {
      client.setQueryData(sessionKey, account)
      client.setQueryData(setupKey, { required: false })
    },
  })
}

export function useSignOut() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(api.DELETE('/session')),
    onSettled: () => client.setQueryData(sessionKey, null),
  })
}

/** Changes the password once the current one is confirmed; the account's other sessions end. */
export function useChangePassword() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { password: string; newPassword: string }) => unwrap(api.PUT('/account/password', { body })),
    onSuccess: () => client.invalidateQueries({ queryKey: sessionKey }),
  })
}

/** Changes the settings of the account, such as how many days a sign-in lasts. */
export function useUpdateAccount() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { sessionDays: 1 | 7 | 30 }) => unwrap(api.PATCH('/account', { body })),
    onSuccess: (account) => client.setQueryData(sessionKey, account),
  })
}

/** Makes a new secret of two-factor authentication, once the password is confirmed. */
export function useTwoFactorSecret() {
  return useMutation({
    mutationFn: (body: { password: string }) => unwrap(api.POST('/account/two-factor/secret', { body })),
  })
}

/** Turns two-factor authentication on with a code of the new secret; the account's other sessions end, and the answer holds the recovery codes, this once. */
export function useEnableTwoFactor() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { code: string }) => unwrap(api.POST('/account/two-factor/enable', { body })),
    onSuccess: () => client.invalidateQueries({ queryKey: sessionKey }),
  })
}

/** Makes new recovery codes once the password is confirmed; the old ones stop working. */
export function useNewRecoveryCodes() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { password: string }) => unwrap(api.POST('/account/two-factor/recovery-codes', { body })),
    onSuccess: () => client.invalidateQueries({ queryKey: sessionKey }),
  })
}

/** Turns two-factor authentication off, once the password is confirmed. */
export function useDisableTwoFactor() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { password: string }) => unwrap(api.POST('/account/two-factor/disable', { body })),
    onSuccess: () => client.invalidateQueries({ queryKey: sessionKey }),
  })
}

const tokensKey = ['account', 'tokens'] as const

/** The account's API tokens, never their values. */
export function useTokens(enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({ queryKey: tokensKey, queryFn: () => unwrap(api.GET('/account/tokens')), enabled })
}

/** Makes an API token once the password is confirmed; the answer holds its value, this once. */
export function useCreateToken() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { name: string; scope: 'read' | 'write'; days?: number; password: string }) => unwrap(api.POST('/account/tokens', { body })),
    onSuccess: () => client.invalidateQueries({ queryKey: tokensKey }),
  })
}

/** Deletes an API token, which stops working at once. */
export function useDeleteToken() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => unwrap(api.DELETE('/account/tokens/{id}', { params: { path: { id } } })),
    onSuccess: () => client.invalidateQueries({ queryKey: tokensKey }),
  })
}

export type Session = components['schemas']['SessionBody']

const sessionsKey = ['account', 'sessions'] as const

/** Where the account is signed in, this session first; only while enabled, as while a dialog shows them. */
export function useSessions(enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: sessionsKey,
    queryFn: async () => (await unwrap(api.GET('/account/sessions'))).sessions,
    enabled: computed(() => toValue(enabled)),
  })
}

/** Signs one session out, or with 'others' every one but this. */
export function useEndSession() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      id === 'others'
        ? unwrap(api.POST('/account/sessions/sign-out-others'))
        : unwrap(api.DELETE('/account/sessions/{id}', { params: { path: { id } } })),
    onSuccess: () => client.invalidateQueries({ queryKey: sessionsKey }),
  })
}
