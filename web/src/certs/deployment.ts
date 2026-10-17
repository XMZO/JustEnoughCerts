// How the UI names where a deployment stands, and finds the deployment
// behind a certificate that failed to be deployed (PLAN.md 4.6).

import type { Certificate, Deployment } from '../api/certs'
import { useProviders } from '../api/providers'
import { useT, type MessageKey, type Params } from '../i18n'

/** The words for where a deployment stands. */
export function deploymentState(d: Deployment): MessageKey {
  if (d.state === 'pending') return d.error ? 'certs.deployment.state.retrying' : 'certs.deployment.state.pending'
  return {
    running: 'certs.deployment.state.running',
    done: 'certs.deployment.state.done',
    failed: 'certs.deployment.state.failed',
    canceled: 'certs.deployment.state.canceled',
  }[d.state] as MessageKey
}

/** failingDeployment is the deployment behind a certificate's deploy_failed state: the first, in the order of its targets, that failed or waits to retry. */
export function failingDeployment(c: Certificate): Deployment | undefined {
  return c.deployments.find((d) => d.state === 'failed' || (d.state === 'pending' && !!d.error))
}

/** useDeployNote gives the words for why a certificate failed to be deployed: the target and the error. */
export function useDeployNote() {
  const { t } = useT()
  const targets = useProviders('target')
  return (c: Certificate): string => {
    const d = failingDeployment(c)
    if (!d?.error) return ''
    const error = t(d.error.key as MessageKey, (d.error.params ?? {}) as Params)
    const target = targets.data.value?.find((p) => p.id === d.target)?.name
    return target ? t('certs.note.deploy_failed', { target, error }) : error
  }
}
