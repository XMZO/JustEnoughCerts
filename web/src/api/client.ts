import createClient from 'openapi-fetch'

import type { MessageKey } from '../i18n'
import type { components, paths } from './schema'

/** The API, relative to the base path the server put into index.html. */
export const api = createClient<paths>({
  baseUrl: new URL('api/v1', document.baseURI).href,
  credentials: 'same-origin',
})

type ErrorBody = components['schemas']['Error']

/** ApiError is an error response: a message key and its arguments. */
export class ApiError extends Error {
  readonly status: number
  readonly code: MessageKey
  readonly params: Record<string, string | number>
  readonly fields: { location: string; code: MessageKey }[]

  constructor(status: number, body: Partial<ErrorBody> | undefined) {
    const code = (body?.code ?? (status >= 500 ? 'kernel.error.internal' : 'kernel.error.invalid_request')) as MessageKey
    super(code)
    this.status = status
    this.code = code
    this.params = (body?.params ?? {}) as Record<string, string | number>
    this.fields = (body?.fields ?? []) as { location: string; code: MessageKey }[]
  }
}

/** networkError stands for a request that got no answer at all. */
export const networkError = new ApiError(0, { code: 'kernel.error.network' })

/**
 * unwrap returns the data of an openapi-fetch result or throws its error as
 * an ApiError, so that TanStack Query sees failures.
 */
export async function unwrap<T>(
  call: Promise<{ data?: T; error?: unknown; response: Response }>,
): Promise<T> {
  let result
  try {
    result = await call
  } catch {
    throw networkError
  }
  if (result.error !== undefined || !result.response.ok) {
    throw new ApiError(result.response.status, result.error as Partial<ErrorBody> | undefined)
  }
  return result.data as T
}
