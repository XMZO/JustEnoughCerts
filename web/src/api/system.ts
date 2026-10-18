import { useMutation, useQuery } from '@tanstack/vue-query'

import { api, ApiError, networkError, unwrap } from './client'
import type { components } from './schema'

export type SystemInfo = components['schemas']['RuntimeBody']

/** Where JEC keeps its data and how it is reached. */
export function useSystem() {
  return useQuery({ queryKey: ['system'], queryFn: () => unwrap(api.GET('/system')), staleTime: Infinity })
}

/** Fetches a copy of the database once the password is confirmed, as the file the server names. */
export function useBackup() {
  return useMutation({
    mutationFn: async (password: string) => {
      let result
      try {
        result = await api.POST('/system/backup', { body: { password }, parseAs: 'blob' })
      } catch {
        throw networkError
      }
      if (result.error !== undefined || !result.response.ok) {
        throw new ApiError(result.response.status, result.error)
      }
      const disposition = result.response.headers.get('Content-Disposition') ?? ''
      return { blob: result.data, name: /filename="([^"]+)"/.exec(disposition)?.[1] ?? 'jec-backup.db' }
    },
  })
}
