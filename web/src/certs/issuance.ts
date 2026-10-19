// How the UI names the parts of an issuance (PLAN.md 4.3): its steps, the
// state of each, and why it started.

import type { Issuance, IssuanceDetail, Step } from '../api/certs'
import type { MessageKey } from '../i18n'

/** The name of each step. */
export const stepText: Record<Step['step'], MessageKey> = {
  key: 'certs.step.key',
  order: 'certs.step.order',
  challenge: 'certs.step.challenge',
  validate: 'certs.step.validate',
  cleanup: 'certs.step.cleanup',
  download: 'certs.step.download',
}

/** How far a step got, for screen readers: the icon shows it otherwise. */
export const stepStateText: Record<Step['state'], MessageKey> = {
  pending: 'certs.step_state.pending',
  running: 'certs.step_state.running',
  done: 'certs.step_state.done',
  failed: 'certs.step_state.failed',
}

/** Why an issuance started. */
export const reasonText: Record<Issuance['reason'], MessageKey> = {
  first: 'certs.reason.first',
  renewal: 'certs.reason.renewal',
  manual: 'certs.reason.manual',
}

/** The words for where an issuance stands. */
export function issuanceState(is: Issuance): MessageKey {
  if (is.state === 'pending') return is.error ? 'certs.issuance.state.retrying' : 'certs.issuance.state.pending'
  return {
    running: 'certs.issuance.state.running',
    waiting: 'certs.issuance.state.waiting',
    done: 'certs.issuance.state.done',
    failed: 'certs.issuance.state.failed',
  }[is.state] as MessageKey
}

/** A deployment of the version an issuance brings, as a step after its own. */
export interface DeployStep {
  target: number
  state: Step['state']
  /** When it ended, if it did. */
  at?: string
  /** The page with its log, once it started. */
  link?: string
}

/**
 * deploySteps are the deployments of the version detail's issuance brings,
 * one for each target of its certificate: under way, done or failed, or
 * to come.
 */
export function deploySteps(detail: IssuanceDetail): DeployStep[] {
  const { issuance: is, certificate: cert } = detail
  return cert.targets.map((target) => {
    const d = cert.deployments.find((x) => x.target === target && !!is.versionId && x.versionId === is.versionId)
    let state: Step['state'] = 'pending'
    if (d?.state === 'done') state = 'done'
    else if (d?.state === 'failed' || (d?.state === 'pending' && d.error)) state = 'failed'
    else if (d?.state === 'running') state = 'running'
    return { target, state, at: d?.finishedAt, link: d ? `/certificates/${cert.id}/deployments/${d.id}` : undefined }
  })
}
