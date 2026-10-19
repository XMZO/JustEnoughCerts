import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'

import { api, unwrap } from './client'
import type { components } from './schema'

export type Feed = components['schemas']['FeedBody']

const key = ['calendar', 'feed'] as const

/** Whether the calendar feed is on, and its path; an error while its module is off. */
export function useFeed() {
  return useQuery({ queryKey: key, queryFn: () => unwrap(api.GET('/calendar/feed')), retry: false })
}

/** Turns the feed on, at a new secret URL, or off. */
export function useSwitchFeed() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (enabled: boolean) => unwrap(api.PUT('/calendar/feed', { body: { enabled } })),
    onSuccess: (feed) => client.setQueryData(key, feed),
  })
}

/** Moves the feed to a new secret URL; the old one stops working. */
export function useRotateFeed() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () => unwrap(api.POST('/calendar/feed/rotate')),
    onSuccess: (feed) => client.setQueryData(key, feed),
  })
}

/** The URL of the feed, its words in lang, for calendar apps. */
export function feedURL(path: string, lang: string): string {
  const url = new URL(path.replace(/^\//, ''), document.baseURI)
  url.searchParams.set('lang', lang)
  return url.href
}
