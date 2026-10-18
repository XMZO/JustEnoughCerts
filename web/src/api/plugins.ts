import { useQuery } from '@tanstack/vue-query'
import { computed, toValue, type MaybeRefOrGetter } from 'vue'

import { addMessages } from '../i18n'
import { api, unwrap } from './client'
import type { components } from './schema'

export type PluginInfo = components['schemas']['PluginBody']

/** The plugins JEC found when it started, once on is true, with the messages they bring. */
export function usePlugins(on: MaybeRefOrGetter<boolean>) {
  return useQuery({
    queryKey: ['plugins'],
    queryFn: async () => {
      const body = await unwrap(api.GET('/plugins/files'))
      for (const p of body.plugins ?? []) addMessages(p.texts)
      return body
    },
    enabled: computed(() => toValue(on)),
    staleTime: 5 * 60_000,
  })
}
