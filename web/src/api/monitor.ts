import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { api, unwrap } from './client'
import { useManifest } from './manifest'
import type { components } from './schema'
import { untilNext } from './certs'

export type MonitorCertificate = components['schemas']['CertificateChecksBody']
export type MonitorResult = components['schemas']['DomainCheckBody']
export type MonitorState = components['schemas']['CheckTallyBody']

const all = ['monitor'] as const

/** Whether the monitor module runs: its API answers only then. */
export function useMonitorOn() {
  const manifest = useManifest()
  return computed(() => !!manifest.data.value?.modules.find((m) => m.id === 'monitor')?.enabled)
}

/** What the domains of certificate id served the last time they were checked. */
export function useMonitor(id: MaybeRefOrGetter<number>) {
  const on = useMonitorOn()
  return useQuery({
    queryKey: computed(() => [...all, 'certificate', toValue(id)] as const),
    queryFn: () => unwrap(api.GET('/monitor/certificates/{id}', { params: { path: { id: toValue(id) } } })),
    enabled: on,
    // Just after the next check, or every minute; back on the tab, at once.
    refetchInterval: (query) => untilNext(query.state.data?.checkAt, 60_000),
    refetchOnWindowFocus: 'always',
  })
}

/** Checks the domains of certificate id now. */
export function useCheckNow(id: MaybeRefOrGetter<number>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(api.POST('/monitor/certificates/{id}/check', { params: { path: { id: toValue(id) } } })),
    onSuccess: (body) => {
      client.setQueryData([...all, 'certificate', toValue(id)], body)
      return client.invalidateQueries({ queryKey: [...all, 'states'] })
    },
  })
}

/** Sets the ports the domains of certificate id are checked on, and checks them. */
export function useSetPorts(id: MaybeRefOrGetter<number>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (ports: number[]) => unwrap(api.PUT('/monitor/certificates/{id}/ports', { params: { path: { id: toValue(id) } }, body: { ports } })),
    onSuccess: (body) => {
      client.setQueryData([...all, 'certificate', toValue(id)], body)
      return client.invalidateQueries({ queryKey: [...all, 'states'] })
    },
  })
}

/** The sums of the checks of every certificate, by certificate ID. */
export function useMonitorStates() {
  const on = useMonitorOn()
  return useQuery({
    queryKey: [...all, 'states'],
    queryFn: async () => {
      const body = await unwrap(api.GET('/monitor/states'))
      return new Map(body.certificates.map((s) => [s.certificateId, s]))
    },
    enabled: on,
  })
}
