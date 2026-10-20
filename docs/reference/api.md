# API

[中文](api.zh-CN.md)

Every function of the panel is available through its HTTP API, at
`<panel URL>/api/v1`. The API is described by an OpenAPI document at
`/api/v1/openapi.json`, which can be read without signing in.

## Authentication

| Method | Description |
|---|---|
| Session cookie | Used by the browser after signing in; requests from other sites that change something are refused (CSRF protection) |
| API token | For scripts and other programs: created under API tokens on the Settings page, and sent as `Authorization: Bearer jec_...` |

API tokens are read-only or read-write, and may work for a number of days
only, after which they get 401. They cannot manage the account (the
password, two-factor authentication, the session length, API tokens and
single sign-on) or restore a backup; these operations require a session.
Changes made with a token are recorded in the audit log with the token's
name.

## Examples

List every certificate:

```sh
curl -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/certs/certificates
```

Issue certificate 1 now:

```sh
curl -X POST -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/certs/certificates/1/issue
```

Export the configuration file without secrets, as for scheduled backups to
Git:

```sh
curl -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/config > jec.yaml
```

## Errors

A failed request returns the matching HTTP status and JSON such as:

```json
{"code": "kernel.error.invalid_request", "params": {}, "fields": [{"location": "body.domains", "code": "kernel.error.field_required"}]}
```

`code` is a message key, `params` holds its arguments, and `fields` points
at the fields in error. The texts of the message keys are in the locale
files under `i18n/locales/`.

## Health check

`GET /healthz` returns 200 while the server is up, without signing in; the
container image's health check, `jec healthcheck`, requests it.
