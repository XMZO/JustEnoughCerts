import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { api, ApiError, unwrap } from './client'
import { queryClient } from './query'

export const sessionKey = ['session'] as const
export const setupKey = ['setup'] as const

export type Account = Awaited<ReturnType<typeof fetchSession>>

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

/** Turns two-factor authentication on with a code of the new secret; the account's other sessions end. */
export function useEnableTwoFactor() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: { code: string }) => unwrap(api.POST('/account/two-factor/enable', { body })),
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
