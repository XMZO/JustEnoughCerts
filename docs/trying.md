# Trying JEC with real accounts

[中文](trying.zh-CN.md)

Alibaba Cloud ESA, ESA's edge answers and Namecheap's SSL Manager have only
been tested against fakes so far (see [providers.md](providers.md)). This
list goes through them step by step with real accounts, for websites on
ESA, DNS at Tencent Cloud's DNSPod and certificates bought from Namecheap,
and says what each step should show and what to note when it does not.
For free certificates only, step 4 is the end.

Try it with a test release or the main branch: replace `latest` with
`beta` for the image in `deploy/docker-compose.yml`. Enter every key in
JEC's pages, never in a file, a screenshot or a chat.

## 1. The keys

- **Alibaba Cloud**: a RAM user with an AccessKey, allowed what deploying
  to ESA takes: `esa:ListSites`, `esa:ListCertificates`,
  `esa:SetCertificate` and `esa:DeleteCertificate`; for edge answers,
  `esa:*Kv*` and `esa:*Routine*` too (`PLAN.md` 5.4 lists each).
- **Tencent Cloud**: a CAM sub-user with `QcloudDNSPodFullAccess`, and an
  API key for it.

Add them on the Credentials page as an Alibaba Cloud AccessKey and a
Tencent Cloud API key. Or skip that: choosing a new credential while
adding the providers below keeps the keys as one.

## 2. The deploy target: Alibaba Cloud ESA

Deploy targets → Add a deploy target → Alibaba Cloud ESA site: the
Alibaba Cloud credential, the China site (`esa.cn-hangzhou.aliyuncs.com`)
and the site's name in ESA. Save, then Test.

- It should say it is connected, and how many certificates the site holds.
- When it says the rights fall short or there is no such site, note what
  it says and the error code in the log.

## 3. The validation: DNSPod

Domain validation → Add → search for Tencent Cloud DNS: the Tencent Cloud
credential. Save, then Test: it should say how many domains the account
manages, yours among them.

## 4. A free certificate first

1. On Certificate sources, add an ACME account with Let's Encrypt.
2. On Certificates, add a certificate for a domain of the ESA site: the
   ACME account as its source, DNSPod as its validation, ESA as its
   target.
3. The issuance page logs each step: the order, the TXT record, the CA's
   check, the certificate. Once issued, it is deployed to ESA.
4. In ESA's console, the site should hold a certificate named `jec-…`;
   after another issuance (Issue now on the certificate's page), the one
   before should be gone, so that the free plan's two places never fill
   up.
5. A few minutes later, the certificate's online check should find the new
   certificate.

When something fails, copy the whole log of the issuance or the
deployment (it holds no keys), and note what ESA's console shows.

## 5. ESA's edge answers (optional)

On Domain validation, add ESA edge answers with the Alibaba Cloud
credential and a domain of the ESA site to test with, then Test. The first
time, JEC makes the Edge KV namespace `jec_validation`, the Edge Routine
`jec-validation` and the site's route of `/.well-known/` to it, which may
take a minute or two; when the test runs out of time, test again a little
later.

Then a certificate can use ESA edge answers as its validation (ACME then
takes HTTP-01). Note whether the routine and the route show in ESA's
console, how long the test took, and any error.

## 6. Namecheap SSL Manager

1. On the Modules page, switch the Namecheap module on (it is marked
   experimental).
2. In Namecheap's panel, activate or reissue the PositiveSSL certificate
   with Server-side automation, and copy the one-time token it gives
   (valid for 24 hours, for one use).
3. On Certificate sources, add Namecheap SSL Manager with the token, save,
   and Test at once: the test registers with the token.
4. Add the certificate: its single domain, Namecheap as its source, and
   ESA edge answers as its validation (or a web root, or JEC answering);
   should Namecheap validate by DNS, DNSPod.
5. The issuance waits for the source, looking at Namecheap every five
   minutes; it may take hours, or a day.

Some things only a real account tells (`PLAN.md` 5.6); please note what
the log shows of them:

- Which of the account's certificates JEC sees once registered: the one
  activated with Server-side automation only, or all.
- What the log's state at Namecheap says between the validation and the
  certificate.
- Whether Namecheap validates by a file over HTTP or by DNS; for DNS,
  whether the record's name is the full one or a relative one.
- Whether waits such as "the CSR did not come from this issuance" or
  "Namecheap has no certificate yet" show up.
- Whether the next certificate, 200 days later, takes a new token.

## 7. What to note, and not to send

- Whether each step went through, and what the page said.
- The whole log of a step that failed.
- The states at Namecheap, and what changed in ESA's console.

Never send the AccessKey, API keys, the one-time token, the `secret.key`
of the data directory or the database.
