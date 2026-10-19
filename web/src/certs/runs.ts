// How the UI tells the issuances and deployments of the history
// (docs/design/boards/History): how each went, the page with its log, and
// the words for it.

import type { Run } from '../api/certs'
import { useProviders } from '../api/providers'
import { useT, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'

/** How a run went, as the history filters and colors it: one waiting to retry has failed. */
export type RunResult = 'ok' | 'failed' | 'running' | 'canceled'

export function runResult(r: Run): RunResult {
  if (r.state === 'done') return 'ok'
  if (r.state === 'failed' || (r.state === 'pending' && r.error)) return 'failed'
  if (r.state === 'canceled') return 'canceled'
  return 'running'
}

/** The page with the log of r. */
export function runLink(r: Run): string {
  return `/certificates/${r.certificate}/${r.kind === 'issuance' ? 'issuances' : 'deployments'}/${r.id}`
}

/** When r is shown as happening: the start of its last attempt, or when it was queued. */
export const runAt = (r: Run): string => r.startedAt ?? r.createdAt

/** The seconds the last attempt of r took, or null while it has not ended. */
export function runSeconds(r: Run): number | null {
  if (!r.startedAt || !r.finishedAt) return null
  return Math.max(0, Math.round((Date.parse(r.finishedAt) - Date.parse(r.startedAt)) / 1000))
}

export const actionText = {
  issue: 'certs.runs.action.issue',
  renew: 'certs.runs.action.renew',
  deploy: 'certs.runs.action.deploy',
} as const satisfies Record<Run['action'], MessageKey>

export const triggerText = {
  auto: 'certs.runs.trigger.auto',
  manual: 'certs.runs.trigger.manual',
} as const satisfies Record<Run['trigger'], MessageKey>

export const resultText = {
  ok: 'certs.runs.result.ok',
  failed: 'certs.runs.result.failed',
  running: 'certs.runs.result.running',
  canceled: 'certs.runs.result.canceled',
} as const satisfies Record<RunResult, MessageKey>

/** The lines of the recent activity on the overview; runs called off are left out of it. */
const activityText = {
  issue: { ok: 'certs.activity.issue.ok', failed: 'certs.activity.issue.failed', running: 'certs.activity.issue.running' },
  renew: { ok: 'certs.activity.renew.ok', failed: 'certs.activity.renew.failed', running: 'certs.activity.renew.running' },
  deploy: { ok: 'certs.activity.deploy.ok', failed: 'certs.activity.deploy.failed', running: 'certs.activity.deploy.running' },
} as const satisfies Record<Run['action'], Record<Exclude<RunResult, 'canceled'>, MessageKey>>

/** seconds says how long a span of s seconds is, in words. */
export function useDurationText() {
  const { t } = useT()
  return (s: number): string => {
    if (s <= 0) return t('certs.runs.instant')
    return s < 60 ? t('certs.runs.seconds', { seconds: s }) : t('certs.runs.minutes', { minutes: Math.floor(s / 60), seconds: s % 60 })
  }
}

/** useRunText gives the words for runs: notes, durations and lines of the recent activity. */
export function useRunText() {
  const { t } = useT()
  const intl = useIntl()
  const targets = useProviders('target')
  const targetName = (id?: number) => targets.data.value?.find((p) => p.id === id)?.name ?? t('certs.runs.target_gone')
  const errorText = (r: Run) => (r.error ? t(r.error.key as MessageKey, (r.error.params ?? {}) as Params) : '')
  return {
    /** note says what there is to know about r: why it failed, where it went, until when the new version holds. */
    note(r: Run): string {
      const result = runResult(r)
      if (result === 'failed') return r.kind === 'deployment' ? t('certs.note.deploy_failed', { target: targetName(r.target), error: errorText(r) }) : errorText(r)
      if (r.kind === 'deployment') return t('certs.runs.note.target', { target: targetName(r.target) })
      if (result === 'ok' && r.notAfter) return t('certs.runs.note.valid_until', { date: intl.date(r.notAfter) })
      return ''
    },
    /** duration says how long the last attempt of r took, or '' until it ends. */
    duration(r: Run): string {
      const s = runSeconds(r)
      if (s === null) return ''
      if (s === 0) return t('certs.runs.instant')
      return s < 60 ? t('certs.runs.seconds', { seconds: s }) : t('certs.runs.minutes', { minutes: Math.floor(s / 60), seconds: s % 60 })
    },
    /** activity is r as a line of the recent activity, or '' for one called off. */
    activity(r: Run): string {
      const result = runResult(r)
      if (result === 'canceled') return ''
      return t(activityText[r.action][result], { domain: r.domain, target: targetName(r.target) })
    },
  }
}
