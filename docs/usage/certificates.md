# Certificates

[中文](certificates.zh-CN.md)

The Certificates page lists every certificate with its state, remaining
validity, source, deploy targets and next renewal; on phones, certificates
that need attention are listed separately with the reason. The states are
described in [Concepts](../guide/concepts.md#certificate-states).

## Adding a certificate

On the Certificates page, choose Add certificate. How a certificate is
added depends on its source:

| Method | Description |
|---|---|
| Issued by a source | Choose a certificate source, enter the domains, and choose a validation method and a key type. Issuance starts on submission, and the certificate is renewed automatically from then on |
| Manual upload | Choose manual upload as the source, then paste PEM text (the certificate, its intermediates and the private key, in any order) or upload a PFX/P12 file with its password. Such certificates are not renewed automatically; reminders are sent before they expire |
| Import a certificate | Choose Import a certificate; this works as a manual upload and suits the migration of existing certificates |

Domains are handled as follows:

- The first domain is the primary domain; wildcards are written as
  `*.example.com`.
- Domains are converted to lowercase, internationalized domain names to
  Punycode, and duplicates are removed.
- Wildcard certificates require DNS validation.
- The same certificate, by fingerprint, can be added only once.

The key type is RSA 2048, 3072 or 4096, or ECDSA P-256 or P-384; the
default is set under Renewal defaults on the Settings page. Every renewal
generates a new private key.

## The certificate's page

The certificate's page shows:

- the remaining validity, the issue and expiry dates, the validity period
  and the next renewal;
- the deploy targets, with the result of the latest deployment to each;
- the steps and duration of the last renewal, or of the latest issuance;
- the result of the online check (see [Renewal](renewal.md#online-checks));
- the versions and the certificate chain;
- the certificate details (domains, issuer, source, key, serial number
  and SHA-256 fingerprint) and the renewal settings.

## Actions

| Action | Description |
|---|---|
| Issue now / Renew now | Starts an issuance at once, without waiting for the renewal time |
| Upload new certificate | Adds a new version. The new certificate must include the primary domain and expire later than the current version |
| Choose targets | Adds or removes deploy targets. A new target receives the current version at once |
| Deploy now / Deploy again | Deploys the current version again, to every target or to one |
| Download | Downloads a ZIP file with `cert.pem`, `chain.pem`, `fullchain.pem` and `privkey.pem`. As it contains the private key, the password is required first |
| Revoke | Has an earlier version revoked at its CA, from the version history, with a reason; the password is required first. See below |
| Delete certificate | Deletes every version and private key of the certificate. Copies already deployed elsewhere are not affected |

Each row of the certificate list also offers the common actions in a menu.

## Revoking

A version can be revoked when an ACME account issued it, it has not
expired, and it is not the version in use. The CA then lists it as
revoked, and clients that check revocation stop trusting it; this cannot
be undone. To revoke the version in use, for example when its private key
has leaked, choose **Issue now** first, and revoke the old version once
the new one is deployed: the sites never serve a revoked certificate.

| Reason | When |
|---|---|
| A newer certificate replaces it | The usual reason for an earlier version |
| Its private key has leaked | The private key was exposed; JEC uses a new key for every version |
| Its domains are no longer in use | The domains were given up |
| No particular reason | None of the above |

Certificates from other sources, such as uploads or vendors' services,
are revoked where they were obtained.
