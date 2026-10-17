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
    mutationFn: (body: { username: string; password: string }) => unwrap(api.POST('/session', { body })),
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
