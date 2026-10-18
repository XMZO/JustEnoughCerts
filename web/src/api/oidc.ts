import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { api, ApiError, unwrap } from './client'
import type { components } from './schema'

// Single sign-on through an OpenID Connect provider, of the oidc module.

export type OidcSettings = components['schemas']['OIDCSettingsBody']
export type OidcSettingsInput = components['schemas']['OidcSettingsInputBody']

const settingsKey = ['oidc', 'settings'] as const

/** Whether the sign-in page offers single sign-on: not while the module is off. */
export function useOidcStatus() {
  return useQuery({
    queryKey: ['oidc', 'status'],
    queryFn: async () => {
      try {
        return await unwrap(api.GET('/oidc/status'))
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) return { enabled: false, label: '' }
        throw err
      }
    },
    staleTime: 60_000,
  })
}

export function useOidcSettings() {
  return useQuery({ queryKey: settingsKey, queryFn: () => unwrap(api.GET('/oidc/settings')) })
}

export function useSaveOidcSettings() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: OidcSettingsInput) => unwrap(api.PUT('/oidc/settings', { body })),
    onSuccess: (s) => client.setQueryData(settingsKey, s),
  })
}

/** The address the provider sends people back to, under JEC's base path. */
export function callbackURL(): string {
  return new URL('api/v1/oidc/callback', document.baseURI).href
}

/** Where the browser goes to sign in at the provider. */
export function loginURL(): string {
  return new URL('api/v1/oidc/login', document.baseURI).href
}
