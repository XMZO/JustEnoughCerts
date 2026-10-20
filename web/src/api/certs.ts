import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, ApiError, networkError, unwrap } from './client'
import type { components, operations } from './schema'

export type Certificate = components['schemas']['CertificateBody']
export type Version = components['schemas']['VersionBody']
export type ChainEntry = components['schemas']['ChainEntry']
export type CertificateState = Certificate['state']
export type Issuance = components['schemas']['IssuanceBody']
export type IssuanceDetail = components['schemas']['IssuanceDetailOutputBody']
export type IssueRequest = components['schemas']['IssueRequest']
export type LogLine = components['schemas']['LogLineBody']
export type Step = components['schemas']['StepBody']
export type KeyType = NonNullable<IssueRequest['keyType']>
export type DeployWindow = NonNullable<NonNullable<operations['certs-update']['requestBody']>['content']['application/json']['deployWindow']>
export type Deployment = components['schemas']['DeploymentBody']
export type Run = components['schemas']['RunBody']
export type DeploymentDetail = components['schemas']['DeploymentDetailOutputBody']
export type CertSettings = components['schemas']['CertSettingsBody']
export type CertSettingsInput = components['schemas']['CertSettingsInputBody']

/** A certificate with its private key: PEM as pasted, or a PKCS #12 file in Base64. */
export interface PEMUpload {
  certificate?: string
  privateKey?: string
  pfx?: string
  pfxPassword?: string
}

const all = ['certs'] as const
const listKey = [...all, 'list'] as const

const settingsKey = [...all, 'settings'] as const

/** The defaults of new certificates, and the time of day renewals start. */
export function useCertSettings() {
  return useQuery({ queryKey: settingsKey, queryFn: () => unwrap(api.GET('/certs/settings')) })
}

/** Saves the settings; the times certificates renew follow. */
export function useSaveCertSettings() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (body: CertSettingsInput) => unwrap(api.PUT('/certs/settings', { body })),
    onSuccess: async (saved) => {
      client.setQueryData(settingsKey, saved)
      await Promise.all([client.invalidateQueries({ queryKey: listKey }), client.invalidateQueries({ queryKey: [...all, 'detail'] })])
    },
  })
}

/** Every certificate, the soonest to expire first. */
export function useCertificates() {
  return useQuery({
    queryKey: listKey,
    queryFn: async () => (await unwrap(api.GET('/certs/certificates'))).certificates ?? [],
  })
}

/** One certificate with its versions and chain, kept current while it is shown. */
export function useCertificate(id: MaybeRefOrGetter<number>) {
  return useQuery({
    queryKey: computed(() => [...all, 'detail', toValue(id)] as const),
    queryFn: () => unwrap(api.GET('/certs/certificates/{id}', { params: { path: { id: toValue(id) } } })),
    // Quick while an issuance or a deployment runs or is about to; just
    // after the next try while one waits; else every minute. Back on the
    // tab, at once.
    refetchInterval: (query) => {
      const c = query.state.data?.certificate
      const is = c?.issuance
      const busy = (state?: string, error?: unknown) => state === 'running' || (state === 'pending' && !error)
      if (busy(is?.state, is?.error) || c?.deployments.some((d) => busy(d.state, d.error))) return 2000
      if (is?.state === 'waiting' || is?.state === 'pending') return untilNext(is.nextAt, 60_000)
      return 60_000
    },
    refetchOnWindowFocus: 'always',
  })
}

function useCertsMutation<V, R>(fn: (vars: V) => Promise<R>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => client.invalidateQueries({ queryKey: all }),
  })
}

/** The issuances and deployments of the last days (0 for all), polled while some are under way. */
export function useRuns(days: MaybeRefOrGetter<number>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => [...all, 'runs', toValue(days)] as const),
    queryFn: () => unwrap(api.GET('/certs/runs', { params: { query: { days: toValue(days) } } })),
    enabled: computed(() => toValue(enabled)),
    refetchInterval: (q) => {
      const runs = q.state.data?.runs ?? []
      if (runs.some((r) => r.state === 'running' || (r.state === 'pending' && !r.error))) return 3000
      return runs.some((r) => r.state === 'waiting') ? 30_000 : false
    },
  })
}

export type RunsQuery = NonNullable<operations['certs-runs']['parameters']['query']>

/** runsPageSize is how many runs a page of the history holds. */
export const runsPageSize = 50

/**
 * A page of the history as q asks: the last days, the action, the result,
 * the text. The page before stays while the next one loads; polled as
 * useRuns is.
 */
export function useRunsPage(q: MaybeRefOrGetter<RunsQuery>) {
  return useQuery({
    queryKey: computed(() => [...all, 'runs', 'page', toValue(q)] as const),
    queryFn: () => unwrap(api.GET('/certs/runs', { params: { query: { size: runsPageSize, ...toValue(q) } } })),
    placeholderData: keepPreviousData,
    refetchInterval: (query) => {
      const runs = query.state.data?.runs ?? []
      if (runs.some((r) => r.state === 'running' || (r.state === 'pending' && !r.error))) return 3000
      return runs.some((r) => r.state === 'waiting') ? 30_000 : false
    },
  })
}

/** Every run q asks for, at most 5000, for an export. */
export async function runsExport(q: RunsQuery): Promise<Run[]> {
  return (await unwrap(api.GET('/certs/runs', { params: { query: { ...q, page: 1, size: 5000 } } }))).runs
}

/** untilNext is how long to wait to look again at what tries again at nextAt: just after it, between two seconds and longest. */
export function untilNext(nextAt?: string | null, longest = 30_000): number {
  const due = nextAt ? Date.parse(nextAt) - Date.now() + 1500 : Number.NaN
  return Number.isNaN(due) ? longest : Math.min(longest, Math.max(2000, due))
}

/** An issuance with its steps and log, polled while it is under way. */
export function useIssuance(id: MaybeRefOrGetter<number>, enabled: MaybeRefOrGetter<boolean> = true) {
  const client = useQueryClient()
  return useQuery({
    queryKey: computed(() => [...all, 'issuance', toValue(id)] as const),
    enabled: computed(() => toValue(enabled)),
    queryFn: async () => {
      const detail = await unwrap(api.GET('/certs/issuances/{id}', { params: { path: { id: toValue(id) } } }))
      if (detail.issuance.state === 'done' || detail.issuance.state === 'failed') {
        // The certificate changed: its pages show it from now on.
        void client.invalidateQueries({ queryKey: listKey })
        void client.invalidateQueries({ queryKey: [...all, 'detail', detail.issuance.certificateId] })
      }
      return detail
    },
    // Quick while an attempt runs or is about to, or the version it issued
    // is being deployed; while a retry or the source waits, again just after
    // the next try. Back on the tab, at once.
    refetchInterval: (query) => {
      const is = query.state.data?.issuance
      if (is?.state === 'running' || (is?.state === 'pending' && !is.error)) return 2000
      const deploying = query.state.data?.certificate.deployments.some(
        (d) => d.versionId === is?.versionId && (d.state === 'running' || (d.state === 'pending' && !d.error)),
      )
      if (is?.state === 'done' && deploying) return 2000
      return is?.state === 'pending' || is?.state === 'waiting' ? untilNext(is.nextAt) : false
    },
    refetchOnWindowFocus: 'always',
  })
}

/** The latest issuances of certificate id. */
export function useIssuances(id: MaybeRefOrGetter<number>, enabled: MaybeRefOrGetter<boolean> = true) {
  return useQuery({
    queryKey: computed(() => [...all, 'issuances', toValue(id)] as const),
    queryFn: async () =>
      (await unwrap(api.GET('/certs/certificates/{id}/issuances', { params: { path: { id: toValue(id) }, query: { limit: 10 } } })))
        .issuances ?? [],
    enabled: computed(() => toValue(enabled)),
    refetchInterval: 60_000,
    refetchOnWindowFocus: 'always',
  })
}

/** A deployment with its log, polled while it is under way. */
export function useDeployment(id: MaybeRefOrGetter<number>) {
  const client = useQueryClient()
  return useQuery({
    queryKey: computed(() => [...all, 'deployment', toValue(id)] as const),
    queryFn: async () => {
      const detail = await unwrap(api.GET('/certs/deployments/{id}', { params: { path: { id: toValue(id) } } }))
      if (detail.deployment.state !== 'running' && detail.deployment.state !== 'pending') {
        void client.invalidateQueries({ queryKey: listKey })
        void client.invalidateQueries({ queryKey: [...all, 'detail', detail.certificate.id] })
      }
      return detail
    },
    refetchInterval: (query) => {
      const d = query.state.data?.deployment
      if (d?.state === 'running' || (d?.state === 'pending' && !d.error)) return 2000
      return d?.state === 'pending' ? untilNext(d.nextAt) : false
    },
    refetchOnWindowFocus: 'always',
  })
}

/** Deploys the current version of certificate id now, to one target or to all of them. */
export function useDeployNow(id: MaybeRefOrGetter<number>) {
  return useCertsMutation((target?: number) =>
    unwrap(api.POST('/certs/certificates/{id}/deploy', { params: { path: { id: toValue(id) } }, body: { target } })),
  )
}

/** Adds a certificate that a source provider issues. */
export function useIssueCertificate() {
  return useCertsMutation((body: IssueRequest) => unwrap(api.POST('/certs/certificates/issue', { body })))
}

/** Starts an issuance of certificate id now, or returns the one under way. */
export function useIssueNow(id: MaybeRefOrGetter<number>) {
  return useCertsMutation(() => unwrap(api.POST('/certs/certificates/{id}/issue', { params: { path: { id: toValue(id) } } })))
}

/** Starts an issuance of the certificate it is given now, as useIssueNow does, for pages of many. */
export function useIssueOf() {
  return useCertsMutation((id: number) => unwrap(api.POST('/certs/certificates/{id}/issue', { params: { path: { id } } })))
}

/** Deploys the current version of the certificate it is given now, to all its targets. */
export function useDeployOf() {
  return useCertsMutation((id: number) => unwrap(api.POST('/certs/certificates/{id}/deploy', { params: { path: { id } }, body: {} })))
}

export type Imported = components['schemas']['ImportedBody']

/** useImportCertificates adds the certificates among files of certificates and keys, as another tool exported them. */
export function useImportCertificates() {
  return useCertsMutation((body: { files: { name: string; content: string }[]; renewBeforeDays?: number; targets?: number[] }) =>
    unwrap(api.POST('/certs/certificates/import', { body })),
  )
}

export function useAddCertificate() {
  return useCertsMutation((body: PEMUpload & { renewBeforeDays?: number; targets?: number[] }) =>
    unwrap(api.POST('/certs/certificates', { body })),
  )
}

export function useUploadVersion(id: MaybeRefOrGetter<number>) {
  return useCertsMutation((body: PEMUpload) =>
    unwrap(api.POST('/certs/certificates/{id}/versions', { params: { path: { id: toValue(id) } }, body })),
  )
}

export function useUpdateCertificate(id: MaybeRefOrGetter<number>) {
  return useCertsMutation((body: {
    renewBeforeDays?: number
    targets?: number[]
    note?: string
    reuseKey?: boolean
    notifyTo?: number[]
    paused?: boolean
    solverProvider?: number
    keyType?: KeyType
    deployWindow?: DeployWindow
    reminder?: { on: string; note?: string }
  }) =>
    unwrap(api.PATCH('/certs/certificates/{id}', { params: { path: { id: toValue(id) } }, body })),
  )
}

/** useSetDomains changes the domains of a certificate with a source, which is issued again at once. */
export function useSetDomains(id: MaybeRefOrGetter<number>) {
  return useCertsMutation((domains: string[]) =>
    unwrap(api.PUT('/certs/certificates/{id}/domains', { params: { path: { id: toValue(id) } }, body: { domains } })),
  )
}

/** Adds a certificate to buy from a CA by hand: JEC makes the key and its CSR, and the certificate waits for the CA's. */
export function useRequestCertificate() {
  return useCertsMutation((body: { domains: string[]; keyType?: KeyType; renewBeforeDays?: number; targets?: number[] }) =>
    unwrap(api.POST('/certs/certificates/request', { body })),
  )
}

/** Makes a new key and CSR for certificate id, uploaded by hand, as to renew one bought. */
export function useNewRequest(id: MaybeRefOrGetter<number>) {
  return useCertsMutation((keyType: KeyType) =>
    unwrap(api.POST('/certs/certificates/{id}/csr', { params: { path: { id: toValue(id) } }, body: { keyType } })),
  )
}

/** Forgets the request certificate id waits on, with its key. */
export function useDropRequest(id: MaybeRefOrGetter<number>) {
  return useCertsMutation(() => unwrap(api.DELETE('/certs/certificates/{id}/csr', { params: { path: { id: toValue(id) } } })))
}

export function useDeleteCertificate(id: MaybeRefOrGetter<number>) {
  return useCertsMutation(() => unwrap(api.DELETE('/certs/certificates/{id}', { params: { path: { id: toValue(id) } } })))
}

/** Deletes the certificate it is given, as several chosen at once are. */
/** Sets the targets of the certificate it is given; a new one gets the version in use at once. */
export function useTargetsOf() {
  return useCertsMutation(({ id, targets }: { id: number; targets: number[] }) =>
    unwrap(api.PATCH('/certs/certificates/{id}', { params: { path: { id } }, body: { targets } })),
  )
}

/** Changes the solver of the certificate it is given, from its next issuance. */
export function useSolverOf() {
  return useCertsMutation(({ id, solverProvider }: { id: number; solverProvider: number }) =>
    unwrap(api.PATCH('/certs/certificates/{id}', { params: { path: { id } }, body: { solverProvider } })),
  )
}

/** Pauses or resumes the renewal of the certificate it is given. */
export function usePauseOf() {
  return useCertsMutation(({ id, paused }: { id: number; paused: boolean }) =>
    unwrap(api.PATCH('/certs/certificates/{id}', { params: { path: { id } }, body: { paused } })),
  )
}

export function useDeleteOf() {
  return useCertsMutation((id: number) => unwrap(api.DELETE('/certs/certificates/{id}', { params: { path: { id } } })))
}

/** The reasons a version can be revoked for. */
export type RevokeReason = 'unspecified' | 'key_compromise' | 'superseded' | 'cessation_of_operation'

/** useRevoke has a version of certificate id revoked at its CA, once the password is confirmed. */
export function useRevoke(id: MaybeRefOrGetter<number>) {
  return useCertsMutation(({ version, password, reason }: { version: number; password: string; reason: RevokeReason }) =>
    unwrap(
      api.POST('/certs/certificates/{id}/versions/{version}/revoke', {
        params: { path: { id: toValue(id), version } },
        body: { password, reason },
      }),
    ),
  )
}

/** useUseVersion goes back to an earlier version of certificate id, which is deployed to every target. */
export function useUseVersion(id: MaybeRefOrGetter<number>) {
  return useCertsMutation((version: number) =>
    unwrap(api.POST('/certs/certificates/{id}/versions/{version}/use', { params: { path: { id: toValue(id), version } } })),
  )
}

/**
 * useDownload fetches a version of certificate id with its private key, as
 * the ZIP the server names, once the password is confirmed.
 */
export function useDownload(id: MaybeRefOrGetter<number>) {
  return useMutation({
    mutationFn: async ({ version, ...body }: { version: number; password: string; pfx?: boolean; pfxPassword?: string; pfxLegacy?: boolean }) => {
      let result
      try {
        result = await api.POST('/certs/certificates/{id}/versions/{version}/download', {
          params: { path: { id: toValue(id), version } },
          body,
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

/** useExportAll downloads the version in use of every certificate with its key, as one ZIP, once the password is confirmed. */
export function useExportAll() {
  return useMutation({
    mutationFn: async (password: string) => {
      let result
      try {
        result = await api.POST('/certs/certificates/export', { body: { password }, parseAs: 'blob' })
      } catch {
        throw networkError
      }
      if (result.error !== undefined || !result.response.ok) {
        throw new ApiError(result.response.status, result.error)
      }
      const disposition = result.response.headers.get('Content-Disposition') ?? ''
      return { blob: result.data, name: /filename="([^"]+)"/.exec(disposition)?.[1] ?? 'certificates.zip' }
    },
  })
}

/** A certificate request of version's private key for its names, which the server makes anew when asked. */
export function useCsr(id: MaybeRefOrGetter<number>, version: MaybeRefOrGetter<number>, enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => [...all, 'csr', toValue(id), toValue(version)] as const),
    queryFn: () =>
      unwrap(api.GET('/certs/certificates/{id}/versions/{version}/csr', { params: { path: { id: toValue(id), version: toValue(version) } } })),
    enabled: computed(() => toValue(enabled)),
    staleTime: Infinity,
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

/** Whether servers can fetch certificate id themselves; only while enabled, as while its dialog shows. */
export function usePull(id: MaybeRefOrGetter<number>, enabled: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: computed(() => ['certs', 'pull', toValue(id)] as const),
    queryFn: () => unwrap(api.GET('/certs/certificates/{id}/pull', { params: { path: { id: toValue(id) } } })),
    enabled: computed(() => toValue(enabled)),
  })
}

/** Makes the key servers fetch certificate id with, once the password is confirmed, or turns pulling off. */
export function usePullKey(id: MaybeRefOrGetter<number>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (password: string | null) =>
      password === null
        ? unwrap(api.DELETE('/certs/certificates/{id}/pull', { params: { path: { id: toValue(id) } } })).then(() => undefined)
        : unwrap(api.POST('/certs/certificates/{id}/pull', { params: { path: { id: toValue(id) } }, body: { password } })),
    onSuccess: () => client.invalidateQueries({ queryKey: ['certs', 'pull', toValue(id)] }),
  })
}

/** pullBase is where servers fetch the files of a certificate. */
export const pullBase = () => new URL('api/v1/certs/pull/', document.baseURI).href
