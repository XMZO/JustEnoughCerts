import { useQuery } from '@tanstack/vue-query'

import { api, unwrap } from './client'

/** The program version and the modules compiled in. */
export function useManifest() {
  return useQuery({
    queryKey: ['manifest'],
    queryFn: () => unwrap(api.GET('/manifest')),
    staleTime: Infinity,
  })
}
