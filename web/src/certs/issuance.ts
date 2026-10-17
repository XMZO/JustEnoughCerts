// How the UI names the parts of an issuance (PLAN.md 4.3): its steps, the
// state of each, and why it started.

import type { Issuance, Step } from '../api/certs'
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
