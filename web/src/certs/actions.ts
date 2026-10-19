// What can be done for a certificate from a page of many, as the
// overview's summary and the certificate list's menus offer it.

import { useDeployOf, useIssueOf, type Certificate } from '../api/certs'
import { useProviders, useProviderTypes } from '../api/providers'
import type { MessageKey } from '../i18n'

/** An action on a certificate: a page to go to, or something to run. */
export interface CertAction {
  label: MessageKey
  to?: string
  run?: () => Promise<unknown>
}

/** useCertActions issues certificates now and deploys them again, and says which of those comes first for one. */
export function useCertActions() {
  const sources = useProviders('source')
  const sourceTypes = useProviderTypes('source')
  const issue = useIssueOf()
  const deploy = useDeployOf()

  /** collects tells a certificate whose source has keys of its own, which collects rather than renews. */
  const collects = (c: Certificate) => {
    const type = sources.data.value?.find((p) => p.id === c.sourceProvider)?.type
    return !!sourceTypes.data.value?.find((s) => s.id === type)?.ownKey
  }

  /** primary is the main thing to do for c; null for an upload, which takes a new version instead. */
  function primary(c: Certificate): CertAction | null {
    if (c.source !== 'provider') return null
    if (c.state === 'issuing' && c.issuance) return { label: 'certs.action.progress', to: `/certificates/${c.id}/issuances/${c.issuance.id}` }
    if (c.state === 'deploy_failed') return { label: 'certs.action.deploy_now', run: () => deploy.mutateAsync(c.id) }
    if (c.state === 'failed') return { label: 'certs.action.retry', run: () => issue.mutateAsync(c.id) }
    return { label: collects(c) ? 'certs.action.collect_now' : 'certs.action.renew_now', run: () => issue.mutateAsync(c.id) }
  }

  return { issue, deploy, collects, primary }
}
