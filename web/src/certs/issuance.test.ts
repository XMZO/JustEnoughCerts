import { describe, expect, it } from 'vitest'

import type { Deployment, IssuanceDetail } from '../api/certs'
import { deploySteps } from './issuance'

const deployment = (d: Partial<Deployment>) => ({ id: 1, target: 1, versionId: 7, state: 'done', ...d }) as Deployment

function detail(versionId: number | undefined, targets: number[], deployments: Deployment[]): IssuanceDetail {
  return { issuance: { versionId }, certificate: { id: 3, targets, deployments } } as unknown as IssuanceDetail
}

describe('deploySteps', () => {
  it('gives each target the deployment of the version the issuance brought', () => {
    const steps = deploySteps(
      detail(7, [1, 2, 3, 4], [
        deployment({ id: 10, target: 1, state: 'done', finishedAt: '2026-10-19T03:02:00Z' }),
        deployment({ id: 11, target: 2, state: 'pending', error: { key: 'certs.error.deploy' } }),
        deployment({ id: 12, target: 3, state: 'running' }),
        deployment({ id: 13, target: 4, versionId: 6, state: 'done' }),
      ]),
    )
    expect(steps).toEqual([
      { target: 1, state: 'done', at: '2026-10-19T03:02:00Z', link: '/certificates/3/deployments/10' },
      { target: 2, state: 'failed', link: '/certificates/3/deployments/11' },
      { target: 3, state: 'running', link: '/certificates/3/deployments/12' },
      { target: 4, state: 'pending' },
    ])
  })

  it('has every deployment to come while the issuance has no version', () => {
    expect(deploySteps(detail(undefined, [1], [deployment({ target: 1 })]))).toEqual([{ target: 1, state: 'pending' }])
  })
})
