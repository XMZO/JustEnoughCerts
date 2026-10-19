// What can be done for a certificate from a page of many, as the
// overview's summary and the certificate list's menus offer it.

import { useDeployOf, useIssueOf, type Certificate } from '../api/certs'
import { useProviders, useProviderTypes } from '../api/providers'
import type { CertAsk } from '../components/CertActionDialog.vue'
import type { MessageKey } from '../i18n'

/** An action on a certificate: a page to go to, or something to run. */
export interface CertAction {
  label: MessageKey
  /** What it does, for a menu, where it stands beside deploying again. */
  hint?: MessageKey
  to?: string
  run?: () => Promise<unknown>
  /** Asks first, with CertActionDialog: it replaces a certificate that works. */
  confirm?: boolean
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
    return {
      label: collects(c) ? 'certs.action.collect_now' : 'certs.action.renew_now',
      hint: collects(c) ? 'certs.menu.collect_hint' : 'certs.menu.renew_hint',
      run: () => issue.mutateAsync(c.id),
      confirm: !!c.current,
    }
  }

  /** ask is what CertActionDialog asks before action, or else renewing, done to c. */
  function ask(c: Certificate, action?: CertAction): CertAsk {
    return { kind: 'issue', certs: [c], collects: collects(c), run: action?.run ?? (() => issue.mutateAsync(c.id)) }
  }

  /** askDeploy is what CertActionDialog asks before deploying c again, to all its targets. */
  const askDeploy = (c: Certificate): CertAsk => ({ kind: 'deploy', certs: [c], run: () => deploy.mutateAsync(c.id) })

  return { issue, deploy, collects, primary, ask, askDeploy }
}
