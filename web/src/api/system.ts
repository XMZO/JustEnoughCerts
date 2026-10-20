import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { api, ApiError, networkError, unwrap } from './client'
import type { components } from './schema'

export type SystemInfo = components['schemas']['RuntimeBody']
export type Health = components['schemas']['HealthBody']

/** Where JEC keeps its data and how it is reached. */
export function useSystem() {
  return useQuery({ queryKey: ['system'], queryFn: () => unwrap(api.GET('/system')), staleTime: Infinity })
}

/**
 * Chooses the time zone of the whole panel, "" for the server's. The times
 * shown follow it, by the manifest, and so do the times renewals start.
 */
export function useSetTimeZone() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (timeZone: string) => unwrap(api.PUT('/system/time-zone', { body: { timeZone } })),
    onSuccess: (info) => {
      client.setQueryData(['system'], info)
      return client.invalidateQueries()
    },
  })
}

/** Chooses how many daily backups JEC makes and keeps in its data directory, 0 for none. */
export function useSetAutoBackup() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (keep: number) => unwrap(api.PUT('/system/auto-backup', { body: { keep } })),
    onSuccess: (info) => client.setQueryData(['system'], info),
  })
}

/** Sets the address JEC asks for while it runs, for a monitoring service; "" for none. */
export function useSetHeartbeat() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (url: string) => unwrap(api.PUT('/system/heartbeat', { body: { url } })),
    onSuccess: (info) => client.setQueryData(['system'], info),
  })
}

export type ReleaseInfo = components['schemas']['ReleaseBody']

/** Asks whether a newer release of JEC is out: only when someone asks, as JEC never calls home on its own. */
export function useReleaseCheck() {
  return useMutation({ mutationFn: () => unwrap(api.GET('/system/release')) })
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

/** Sends a backup to restore, once the password is confirmed: JEC then stops, to start again with it. */
export function useRestore() {
  return useMutation({
    mutationFn: (input: { file: File; password: string }) => {
      const form = new FormData()
      form.append('file', input.file)
      form.append('password', input.password)
      return unwrap(api.POST('/system/restore', { body: form as never, bodySerializer: (body) => body as unknown as FormData }))
    },
  })
}

/**
 * Waits for JEC to answer again once it stopped to restore a backup: until
 * its health check fails and then passes, or passes after ten seconds, as
 * a quick restart may never be seen down. False if it is not back in two
 * minutes.
 */
export async function waitForRestart(): Promise<boolean> {
  const health = new URL('healthz', document.baseURI).href
  const up = async () => {
    try {
      return (await fetch(health, { cache: 'no-store' })).ok
    } catch {
      return false
    }
  }
  const started = Date.now()
  let down = false
  while (Date.now() - started < 120_000) {
    await new Promise((resolve) => setTimeout(resolve, 1500))
    if (!(await up())) down = true
    else if (down || Date.now() - started > 10_000) return true
  }
  return false
}

/** lowDisk tells a file system with less than a gigabyte, or a twentieth, left. */
export const lowDisk = (d?: Health['disk']) => !!d && (d.free < 1024 ** 3 || d.free < d.total / 20)

/** healthTrouble tells a health that someone should look at: a job whose last run failed, or little space left. */
export const healthTrouble = (h?: Health) => !!h && (h.jobs.some((j) => j.error) || lowDisk(h.disk))

/** How the background jobs went last, and the space left for the data; looked at again each minute. */
export function useHealth() {
  return useQuery({ queryKey: ['system', 'health'], queryFn: () => unwrap(api.GET('/system/health')), refetchInterval: 60_000 })
}
