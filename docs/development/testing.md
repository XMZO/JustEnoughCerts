# Testing with real accounts

[中文](testing.zh-CN.md)

Deployment to Alibaba Cloud ESA and Namecheap SSL Manager were verified
with a real account on 2026-10-19 (see section 6 for the results); ESA
edge answers have so far been tested only against fakes; see
[Providers and test coverage](../reference/providers.md). This guide describes how to
verify them with real accounts, using the setup below as an example, and
states the expected result of each step and what to record when it fails:

- the website is hosted on Alibaba Cloud ESA;
- its DNS is hosted at Tencent Cloud DNSPod;
- the certificate is bought from Namecheap.

For free certificates only, steps 1 to 4 are sufficient.

## Before you begin

- Deploy JEC as [Getting started](../guide/getting-started.md) describes.
  `deploy/docker-compose.yml` runs the newest test release (the image tag
  `beta`); to update a running deployment, run
  `docker compose pull && docker compose up -d` in the directory of
  `docker-compose.yml` (see [Upgrades](../guide/installation.md#upgrades)).
  The version under About on the Settings page must be `0.1.0-alpha.10`
  or later.
- Add a notification channel, such as Telegram or email, on the
  Notifications page, so that the results of issuances and deployments
  reach you.
- Enter every key in JEC's web interface only, never in a file, a
  screenshot or a chat message.

## 1. Create the access keys

| Platform | Action | Permissions |
|---|---|---|
| Alibaba Cloud | Create a RAM user and an AccessKey | `esa:ListSites`, `esa:ListCertificates`, `esa:SetCertificate`, `esa:DeleteCertificate`; for edge answers, also `esa:*Kv*` and `esa:*Routine*` (detailed in `PLAN.md` 5.4) |
| Tencent Cloud | Create a CAM sub-user and an API key | `QcloudDNSPodFullAccess` |

On the Credentials page, add an Alibaba Cloud AccessKey and a Tencent
Cloud API key. Alternatively, choose a new credential when adding the
providers below; the keys entered are then saved as a credential.

## 2. Add the deploy target: Alibaba Cloud ESA

1. On the Deploy targets page, choose Add a target → Alibaba Cloud ESA
   site.
2. Select the Alibaba Cloud credential, the China endpoint
   (`esa.cn-hangzhou.aliyuncs.com`) and the site's name in ESA.
3. Save, then choose Test.

Expected result: the connection succeeds and the number of certificates on
the site is shown. If the test reports insufficient permissions or a
missing site, record the message and the error code in the log.

A site on the free plan holds at most two custom certificates, and JEC
removes only those it deployed itself. If the site already holds two,
delete one that is no longer in use in the ESA console first; otherwise
the first deployment fails because the site is full.

## 3. Add the validation method: DNSPod

1. On the Domain validation page, choose Add a validation method, search
   for "Tencent Cloud DNS" and select the Tencent Cloud credential.
2. Save, then choose Test.

Expected result: the connection succeeds and the domains the account
manages are listed, the domain under test among them.

## 4. Issue a free certificate

1. On the Certificate sources page, add an ACME account with Let's Encrypt
   as its certificate authority.
2. On the Certificates page, add a certificate for a domain of the ESA
   site, with the ACME account as its source, DNSPod as its validation
   method and ESA as its deploy target.
3. The issuance page logs each step: placing the order, adding the TXT
   record, the validation by the CA and downloading the certificate. The
   certificate is then deployed to ESA automatically.
4. The ESA console should list a certificate whose name starts with
   `jec-`. After a second issuance (Issue now on the certificate's page),
   the previous certificate should be removed, so that the two certificate
   slots of the free plan never fill up.
5. After a few minutes, the online check on the certificate's page should
   report the new certificate.
6. Optionally, test revocation: once the online check reports the new
   certificate, choose Revoke for the previous version, marked as
   replaced, under Versions on the certificate's page; select the reason
   "A newer certificate replaces it" and confirm with your password.
   Expected result: the version shows "Revoked on ...: A newer
   certificate replaces it". Revocation cannot be undone, and JEC does
   not revoke the version in use.

If a step fails, copy the complete log from the issuance or deployment page
(logs contain no secrets) and record what the ESA console shows.

## 5. ESA edge answers (optional)

1. On the Domain validation page, add "Answered at ESA's edge" with the
   Alibaba Cloud credential, enter a domain of the ESA site as the domain
   to test with, and choose Test.
2. The first test creates the Edge KV namespace `jec_validation`, the Edge
   Routine `jec-validation` and the site's route for `/.well-known/`, which
   may take one to two minutes. If the test times out, run it again later.
3. Once the test passes, add another certificate with "Answered at ESA's
   edge" as its validation method; ACME then uses HTTP-01.

Record whether the routine and the route appear in the ESA console, how
long the test took, and any errors.

## 6. Namecheap SSL Manager

1. On the Modules page, enable the Namecheap module, which is marked
   Experimental.
2. In the Namecheap dashboard, activate or reissue the PositiveSSL
   certificate with Server-side automation, and copy the one-time token it
   provides (valid for 24 hours and for a single use).
3. On the Certificate sources page, add Namecheap SSL Manager with the
   one-time token, save, and choose Test immediately; the test performs the
   registration.
4. Add a certificate for the domain of that certificate (single-domain
   certificates only), with Namecheap as its source and a validation
   method that places files on the website, such as "Answered at ESA's
   edge", "Answered at Cloudflare's edge", a web root or JEC answering.
5. The issuance enters the "Waiting on the source" state and checks
   Namecheap every five minutes. JEC first places SSL Manager's marker
   file on the website; Namecheap lists the certificate only once it
   detects that file. JEC then submits the CSR and places the validation
   files.

Results of 2026-10-19: Namecheap validated by files over HTTP, one for the
domain and one for `www`, and the certificate also covers `www`; it was
issued about six minutes after the files were placed, by SSL.com, valid
for 198 days. `PLAN.md` 5.6 records the details.

The following remain to be confirmed; record what the log shows:

- Which certificates of the account JEC sees after registering: only the
  one activated with Server-side automation, or all of them.
- When the next certificate, about 200 days later, appears, and whether
  it requires a new token.

## 7. What to report

Please report:

- the result of each step and the messages shown;
- the complete log of each failed step, copied from the issuance or
  deployment page;
- the server log around the time of the failure: `docker logs jec` for
  Docker, `journalctl -u jec` for the systemd service. Background errors,
  such as a failed refresh of Namecheap's access token, appear only there;
- the states at Namecheap and the changes in the ESA console.

Never send AccessKeys, API keys, one-time tokens, the `secret.key` file of
the data directory or the database.
