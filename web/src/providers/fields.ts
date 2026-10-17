// How the UI shows provider types and their settings (PLAN.md 5.5): forms
// are built from the fields a type declares, so a new provider needs no
// code here.

import type { Provider, ProviderField, ProviderKind, ProviderType, ProviderTypeSummary } from '../api/providers'
import type { MessageKey } from '../i18n'

/** The page of each kind of provider there is a page for. */
export const kindPages = {
  source: {
    path: '/sources',
    title: 'kernel.providers.source.title',
    intro: 'kernel.providers.source.intro',
    add: 'kernel.providers.source.add',
    empty: 'kernel.providers.source.empty',
  },
  solver: {
    path: '/dns',
    title: 'kernel.providers.solver.title',
    intro: 'kernel.providers.solver.intro',
    add: 'kernel.providers.solver.add',
    empty: 'kernel.providers.solver.empty',
  },
  target: {
    path: '/targets',
    title: 'kernel.providers.target.title',
    intro: 'kernel.providers.target.intro',
    add: 'kernel.providers.target.add',
    empty: 'kernel.providers.target.empty',
  },
} as const satisfies Partial<Record<ProviderKind, Record<string, string>>>

export type PageKind = keyof typeof kindPages

/**
 * typeName is what a type is called: a third party's own name (brand),
 * which is shown as it is, or a message.
 */
export function typeName(type: ProviderTypeSummary | ProviderType | undefined): { brand?: string; key?: MessageKey } {
  if (!type) return {}
  if (type.brand) return { brand: type.brand }
  return type.name ? { key: type.name as MessageKey } : { brand: type.id }
}

/** applies tells whether a field is part of the form, given the values so far. */
export function applies(field: ProviderField, values: Record<string, string>, fields: ProviderField[]): boolean {
  if (!field.when) return true
  const other = fields.find((f) => f.key === field.when?.key)
  return (values[field.when.key] || other?.default || '') === field.when.value
}

/**
 * initialValues are the values a form starts with: an instance's settings,
 * or the defaults, where a language is the one the UI speaks.
 */
export function initialValues(type: ProviderType, instance?: Provider, locale?: string): Record<string, string> {
  const values: Record<string, string> = {}
  for (const f of type.fields) {
    if (f.secret) values[f.key] = ''
    else if (instance) values[f.key] = instance.config[f.key] ?? ''
    else values[f.key] = (f.kind === 'locale' && locale) || (f.default ?? '')
  }
  return values
}

/**
 * settings splits the values of a form into plain settings and secrets.
 * Secrets left empty are not sent, so that an edit keeps the saved ones.
 */
export function settings(type: ProviderType, values: Record<string, string>): { config: Record<string, string>; secrets: Record<string, string> } {
  const config: Record<string, string> = {}
  const secrets: Record<string, string> = {}
  for (const f of type.fields) {
    const v = values[f.key] ?? ''
    if (!applies(f, values, type.fields)) continue
    if (f.secret) {
      if (v.trim()) secrets[f.key] = v
    } else if (v.trim()) {
      config[f.key] = v
    }
  }
  return { config, secrets }
}

/** A setting as a card shows it. */
export interface Shown {
  label?: MessageKey
  key: string
  value?: string
  option?: MessageKey
}

/**
 * summary lists the plain settings of instance worth showing on its card:
 * the ones with a value to read, outside the advanced ones, at most two.
 */
export function summary(type: ProviderType | undefined, instance: Provider): Shown[] {
  if (!type) return []
  const out: Shown[] = []
  for (const f of type.fields) {
    const v = instance.config[f.key]
    if (f.secret || f.advanced || f.kind === 'switch' || f.kind === 'locale' || !v || !applies(f, instance.config, type.fields)) continue
    const option = f.options?.find((o) => o.value === v)
    out.push({ label: f.label as MessageKey | undefined, key: f.key, value: option ? undefined : v, option: option?.label as MessageKey | undefined })
    if (out.length === 2) break
  }
  return out
}
