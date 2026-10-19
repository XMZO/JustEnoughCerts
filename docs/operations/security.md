# Accounts and security

[中文](security.zh-CN.md)

JEC has a single administrator account, created with the setup token on
first access. Its settings are under Account and security on the Settings
page.

## Password and sign-in

- The password is stored as an argon2id hash.
- After five failed sign-ins in a row from one client, that client is
  locked out for 15 minutes. Behind a reverse proxy, set
  `JEC_TRUSTED_PROXIES`, or the lockout applies to everyone behind the
  proxy; see the
  [Configuration reference](configuration.md#behind-a-reverse-proxy).
- Sign-ins last one, seven or 30 days, seven by default.
- Changing the password signs the account out everywhere else.

## Two-factor authentication

With two-factor authentication turned on in the Settings page, signing in
also requires the 6-digit code (TOTP) of an authenticator app. Turning it
on signs the account out everywhere else.

## Resetting the password

If the password or the authenticator is lost, reset the password on the
server. This also turns two-factor authentication off, ends every session
and deletes every API token:

```sh
docker exec -it jec /jec reset-password
```

## Single sign-on

The single sign-on (OIDC) module signs in through an OpenID Connect
provider, such as Authelia, Authentik, Keycloak or Google; the password
keeps working:

1. At the provider, create a client for JEC and register the callback
   address the Settings page shows.
2. Under Single sign-on (OIDC) on the Settings page, enter the issuer, the
   client ID and the client secret (a public client needs none: PKCE
   guards the sign-in), and who may sign in: one email address verified by
   the provider, or one subject identifier (sub), per line.
3. Once single sign-on is switched on, the sign-in page shows its button.

## API tokens

Under API tokens on the Settings page, create read-only or read-write
tokens. Tokens are sent as bearer tokens; see [API](../reference/api.md).
No token can change the password, two-factor authentication or the tokens.

## Audit log

The Audit log page records every operation performed through the panel
(any request other than GET and HEAD, such as signing in, changing
settings, downloading a private key or exporting the configuration with its
secrets), with the account, the client address, the client and the result.
Entries are kept for one year, and can be filtered and exported as CSV.

## Other safeguards

- Private keys and credentials are encrypted with AES-256-GCM, and saved
  secrets are never shown again; downloading a private key requires the
  password again.
- Every response carries security headers, including a Content Security
  Policy; with `JEC_HSTS=true`, HSTS as well.
- The session cookie is protected against CSRF; with an `https`
  `JEC_BASE_URL`, it is sent over HTTPS only.
- Notification channels and deploy targets whose address the user enters
  connect to public addresses only by default, so that they cannot be used
  to reach private networks.
