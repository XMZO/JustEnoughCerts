import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { api, unwrap } from './client'
import type { components } from './schema'

export type ModuleInfo = components['schemas']['ModuleInfo']

/** The program version and the modules compiled in. */
export function useManifest() {
  return useQuery({
    queryKey: ['manifest'],
    queryFn: () => unwrap(api.GET('/manifest')),
    staleTime: Infinity,
  })
}

/** Switches a module on or off. What the modules offer changes with it, such as the provider types. */
export function useSwitchModule() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, on }: { id: string; on: boolean }) => unwrap(api.PUT('/modules/{id}', { params: { path: { id } }, body: { on } })),
    onSuccess: () => client.invalidateQueries(),
  })
}
