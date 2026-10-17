import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, ApiError, networkError, unwrap } from './client'
import type { components } from './schema'

export type Certificate = components['schemas']['CertificateBody']
export type Version = components['schemas']['VersionBody']
export type ChainEntry = components['schemas']['ChainEntry']
export type CertificateState = Certificate['state']

/** A certificate with its PEM private key, as pasted. */
export interface PEMUpload {
  certificate: string
  privateKey?: string
}

const all = ['certs'] as const
const listKey = [...all, 'list'] as const

/** Every certificate, the soonest to expire first. */
export function useCertificates() {
  return useQuery({
    queryKey: listKey,
    queryFn: async () => (await unwrap(api.GET('/certs/certificates'))).certificates ?? [],
  })
}

/** One certificate with its versions and chain. */
export function useCertificate(id: MaybeRefOrGetter<number>) {
  return useQuery({
    queryKey: computed(() => [...all, 'detail', toValue(id)] as const),
    queryFn: () => unwrap(api.GET('/certs/certificates/{id}', { params: { path: { id: toValue(id) } } })),
  })
}

function useCertsMutation<V, R>(fn: (vars: V) => Promise<R>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => client.invalidateQueries({ queryKey: all }),
  })
}

export function useAddCertificate() {
  return useCertsMutation((body: PEMUpload & { renewBeforeDays?: number }) =>
    unwrap(api.POST('/certs/certificates', { body })),
  )
}

export function useUploadVersion(id: MaybeRefOrGetter<number>) {
  return useCertsMutation((body: PEMUpload) =>
    unwrap(api.POST('/certs/certificates/{id}/versions', { params: { path: { id: toValue(id) } }, body })),
  )
}

export function useUpdateCertificate(id: MaybeRefOrGetter<number>) {
  return useCertsMutation((body: { renewBeforeDays: number }) =>
    unwrap(api.PATCH('/certs/certificates/{id}', { params: { path: { id: toValue(id) } }, body })),
  )
}

export function useDeleteCertificate(id: MaybeRefOrGetter<number>) {
  return useCertsMutation(() => unwrap(api.DELETE('/certs/certificates/{id}', { params: { path: { id: toValue(id) } } })))
}

/**
 * useDownload fetches a version of certificate id with its private key, as
 * the ZIP the server names, once the password is confirmed.
 */
export function useDownload(id: MaybeRefOrGetter<number>) {
  return useMutation({
    mutationFn: async ({ version, password }: { version: number; password: string }) => {
      let result
      try {
        result = await api.POST('/certs/certificates/{id}/versions/{version}/download', {
          params: { path: { id: toValue(id), version } },
          body: { password },
          parseAs: 'blob',
        })
      } catch {
        throw networkError
      }
      if (result.error !== undefined || !result.response.ok) {
        throw new ApiError(result.response.status, result.error)
      }
      const disposition = result.response.headers.get('Content-Disposition') ?? ''
      return { blob: result.data, name: /filename="([^"]+)"/.exec(disposition)?.[1] ?? 'certificate.zip' }
    },
  })
}

/** saveFile hands blob to the browser as a download called name. */
export function saveFile(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = name
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
