import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { api, unwrap } from './client'
import type { components } from './schema'

// Copies of the daily backups in an S3 compatible bucket, of the offsite module.

export type OffsiteSettings = components['schemas']['OffsiteSettingsBody']

const settingsKey = ['offsite', 'settings'] as const

/** Where the backups are copied to, and how the last copy went; looked at again each minute. */
export function useOffsiteSettings() {
  return useQuery({ queryKey: settingsKey, queryFn: () => unwrap(api.GET('/offsite/settings')), refetchInterval: 60_000 })
}

/** Saves where the backups are copied to; switched on, the server tries the bucket first. */
export function useSaveOffsite() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: {
      enabled: boolean
      endpoint: string
      region: string
      bucket: string
      prefix: string
      pathStyle: boolean
      accessKey: string
      secretKey?: string
    }) => unwrap(api.PUT('/offsite/settings', { body })),
    onSuccess: (s) => client.setQueryData(settingsKey, s),
  })
}
