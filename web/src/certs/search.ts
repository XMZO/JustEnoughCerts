/** searchTerm is what a search box holds, as searched: lowercase, and a URL pasted in by its host. */
export function searchTerm(input: string): string {
  return input
    .trim()
    .toLowerCase()
    .replace(/^[a-z][a-z0-9+.-]*:\/\/([^/?#:]+).*$/, '$1')
}

/** covers tells a wildcard domain that covers the host q, as *.shop.com covers api.shop.com. */
export function covers(domain: string, q: string): boolean {
  if (!domain.startsWith('*.') || !q.endsWith(domain.slice(1))) return false
  const label = q.slice(0, q.length - domain.length + 1)
  return label !== '' && !label.includes('.')
}

/**
 * matches tells a certificate a search term finds: by a part of a domain, by a host one
 * of its wildcards covers, or by its note.
 */
export function matches(c: { domains: string[]; note?: string }, q: string): boolean {
  return !q || c.domains.some((d) => d.includes(q) || covers(d, q)) || (c.note ?? '').toLowerCase().includes(q)
}
