# Configuration file

[中文](config-file.zh-CN.md)

Under Settings → Data and backups → Configuration file, the credentials,
providers, certificates, renewal defaults and time zone can be exported as
a YAML file, and such a file can be imported, to keep the configuration in
Git or to move it to another instance. This file is unrelated to
`config.yaml`, which configures the server itself.

## Format

Certificates refer to providers by name:

```yaml
version: 1
providers:
  - name: Let's Encrypt
    type: acme.account
    config:
      ca: letsencrypt
      email: me@example.com
  - name: DNSPod
    type: lego.tencentcloud
    secrets:
      TENCENTCLOUD_SECRET_ID: AKID...
      TENCENTCLOUD_SECRET_KEY: ...
  - name: ESA
    type: esa.site
    config:
      region: cn-hangzhou
      access_key_id: LTAI...
      site: example.com
time_zone: Asia/Shanghai
renewal:
  renew_before_days: 30
  check_time: "02:00"
  key_type: rsa-2048
  retries: 7
certificates:
  - domains: [example.com, "*.example.com"]
    source: Let's Encrypt
    solver: DNSPod
    targets: [ESA]
```

| Section | Contents |
|---|---|
| `version` | The version of the format, currently 1 |
| `credentials` | Credentials: name, kind and plain settings (`config`), with their secrets (`secrets`) in an export that includes them |
| `providers` | Providers: name, type, the name of the credential they use (`credential`) and plain settings (`config`), with their secrets (`secrets`) in an export that includes them |
| `time_zone` | The panel's time zone; omitted while it follows the server, and an empty string imported makes it follow the server again |
| `renewal` | The renewal defaults: days before expiry, daily check time, default key type and number of retries |
| `certificates` | Certificates: domains, source, validation method, backup source (`backup_source`), key type, renewal days and deploy targets |

An export holds nothing that changes from one export to the next, such as
its time, so its diff in Git shows only real changes.

## Import rules

- An import adds what JEC lacks and updates what it has: providers are
  matched by kind and name, certificates by their domains.
- It deletes nothing. Secrets missing from the file stay unchanged (a new
  provider needs its secrets in the file), and an existing certificate
  keeps its source.
- It lists every change first, and changes nothing while any entry has an
  error.
- A file with unknown keys, misspelled case included, or of a newer
  format version than the program is rejected as a whole.
- The section of a disabled module is skipped, with a warning.

## Export

Exports omit the secrets by default; an export with the secrets requires
the password and is recorded in the audit log.

## API

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/config` | Export without secrets; a read-only token suffices, which suits scheduled exports to Git |
| `POST /api/v1/config/export` | Export with secrets; requires the password |
| `POST /api/v1/config/check` | Preview the changes a file would make |
| `POST /api/v1/config/import` | Import a file |
