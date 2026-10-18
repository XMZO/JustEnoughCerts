import { useMutation, useQueryClient } from '@tanstack/vue-query'

import { api, ApiError, networkError, unwrap } from './client'
import type { components } from './schema'

export type ConfigReport = components['schemas']['Report']
export type ConfigItem = components['schemas']['Item']

/** The file an answer carries, with the name the server gives it. */
async function received(call: Promise<{ data?: unknown; error?: unknown; response: Response }>): Promise<{ blob: Blob; name: string }> {
  let result
  try {
    result = await call
  } catch {
    throw networkError
  }
  if (result.error !== undefined || !result.response.ok) {
    throw new ApiError(result.response.status, result.error as ConstructorParameters<typeof ApiError>[1])
  }
  const disposition = result.response.headers.get('Content-Disposition') ?? ''
  return { blob: result.data as Blob, name: /filename="([^"]+)"/.exec(disposition)?.[1] ?? 'jec-config.yaml' }
}

/**
 * Fetches the configuration file (PLAN.md 11): without secrets, or with
 * them once the password is confirmed.
 */
export function useExportConfig() {
  return useMutation({
    mutationFn: (password?: string) =>
      received(password === undefined ? api.GET('/config', { parseAs: 'blob' }) : api.POST('/config/export', { body: { password }, parseAs: 'blob' })),
  })
}

/** Sends a configuration file, to tell what it would change, or to change it. */
export function useImportConfig() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { file: string; apply: boolean }) =>
      unwrap(api.POST(input.apply ? '/config/import' : '/config/check', { body: { file: input.file } })),
    onSuccess: (report) => {
      if (report.applied) void client.invalidateQueries()
    },
  })
}
