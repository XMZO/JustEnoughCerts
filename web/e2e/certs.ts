// Self-signed ECDSA certificates for the e2e tests, with any names and any
// validity, made with WebCrypto and a small DER encoder: no openssl needed.
import { webcrypto } from 'node:crypto'

const { subtle } = webcrypto

function concat(parts: Uint8Array[]): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0))
  let i = 0
  for (const p of parts) {
    out.set(p, i)
    i += p.length
  }
  return out
}

function length(n: number): Uint8Array {
  if (n < 0x80) return Uint8Array.of(n)
  const bytes: number[] = []
  for (let v = n; v > 0; v >>= 8) bytes.unshift(v & 0xff)
  return Uint8Array.of(0x80 | bytes.length, ...bytes)
}

const tlv = (tag: number, ...parts: Uint8Array[]) => {
  const body = concat(parts)
  return concat([Uint8Array.of(tag), length(body.length), body])
}
const seq = (...parts: Uint8Array[]) => tlv(0x30, ...parts)
const set = (...parts: Uint8Array[]) => tlv(0x31, ...parts)
const explicit = (n: number, ...parts: Uint8Array[]) => tlv(0xa0 + n, ...parts)
const ascii = (s: string) => new TextEncoder().encode(s)

function int(bytes: Uint8Array): Uint8Array {
  let i = 0
  while (i < bytes.length - 1 && bytes[i] === 0) i++
  const b = bytes.subarray(i)
  return tlv(0x02, (b[0] ?? 0) & 0x80 ? concat([Uint8Array.of(0), b]) : b)
}

function oid(dotted: string): Uint8Array {
  const [a = 0, b = 0, ...rest] = dotted.split('.').map(Number)
  const out = [40 * a + b]
  for (const n of rest) {
    const groups: number[] = []
    for (let v = n; ; v >>= 7) {
      groups.unshift((v & 0x7f) | (groups.length ? 0x80 : 0))
      if (v < 0x80) break
    }
    out.push(...groups)
  }
  return tlv(0x06, Uint8Array.from(out))
}

function time(d: Date): Uint8Array {
  const iso = d.toISOString().replace(/[-:T]/g, '').slice(0, 14) // YYYYMMDDHHMMSS
  return d.getUTCFullYear() < 2050 ? tlv(0x17, ascii(`${iso.slice(2)}Z`)) : tlv(0x18, ascii(`${iso}Z`))
}

function pem(type: string, der: Uint8Array): string {
  const b64 = Buffer.from(der).toString('base64')
  return `-----BEGIN ${type}-----\n${b64.match(/.{1,64}/g)?.join('\n')}\n-----END ${type}-----\n`
}

const day = 86_400_000

/** selfSigned returns a certificate for names valid from `from` to `to` days from now, and its key. */
export async function selfSigned(names: string[], from: number, to: number): Promise<{ certificate: string; privateKey: string }> {
  const keys = await subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify'])
  const ecdsaSHA256 = seq(oid('1.2.840.10045.4.3.2'))
  const name = seq(set(seq(oid('2.5.4.3'), tlv(0x0c, ascii(names[0] ?? 'example.com')))))
  const san = seq(...names.map((n) => tlv(0x82, ascii(n))))
  const tbs = seq(
    explicit(0, int(Uint8Array.of(2))),
    int(webcrypto.getRandomValues(new Uint8Array(8))),
    ecdsaSHA256,
    name,
    seq(time(new Date(Date.now() + from * day)), time(new Date(Date.now() + to * day))),
    name,
    new Uint8Array(await subtle.exportKey('spki', keys.publicKey)),
    explicit(3, seq(seq(oid('2.5.29.17'), tlv(0x04, san)))),
  )
  const raw = new Uint8Array(await subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, keys.privateKey, tbs))
  const signature = seq(int(raw.subarray(0, 32)), int(raw.subarray(32)))
  const cert = seq(tbs, ecdsaSHA256, tlv(0x03, Uint8Array.of(0), signature))
  return {
    certificate: pem('CERTIFICATE', cert),
    privateKey: pem('PRIVATE KEY', new Uint8Array(await subtle.exportKey('pkcs8', keys.privateKey))),
  }
}
