# Monitoring with Prometheus

[中文](monitoring.zh-CN.md)

JEC serves its certificates as metrics for Prometheus at
`/api/v1/metrics/prometheus`, so that dashboards and alerts can follow
them alongside other services. The module "Prometheus metrics" provides
them and can be switched off on the Modules page.

## Scraping

1. Under Settings, API tokens, create a token that may only read.
2. Add a scrape job to Prometheus:

```yaml
scrape_configs:
  - job_name: jec
    scheme: https
    metrics_path: /api/v1/metrics/prometheus
    authorization:
      credentials: jec_…
    static_configs:
      - targets: ["certs.example.com"]
```

Under a sub-path, the path starts with it, such as
`/certs/api/v1/metrics/prometheus`.

## Metrics

Each metric of a certificate has the labels `id` and `domain`, its primary
domain.

| Metric | Meaning |
|---|---|
| `jec_certificate_expiry_timestamp_seconds` | When the version in use ends, in Unix seconds |
| `jec_certificate_renewal_timestamp_seconds` | When the next renewal starts |
| `jec_certificate_state` | 1 for the state the certificate is in (label `state`), 0 for the others |
| `jec_certificate_targets` | The deploy targets of the certificate |
| `jec_certificate_deployments_failed` | The targets whose latest deployment failed |
| `jec_certificate_served_old` | The domains that still serve an older certificate (with the online check module) |
| `jec_certificate_unreachable` | The domains that answer nothing over TLS on port 443 (likewise) |
| `jec_provider_test_ok` | 1 when the last test of a provider (the daily check, or a click) passed, 0 when it failed; those not tested since their settings changed are left out; labels `id`, `name`, `kind` |
| `jec_certificate_chain_incomplete` | The domains that serve the current version without its intermediates (likewise) |
| `jec_certificates` | Certificates by state |
| `jec_build_info` | The version of JEC, in the label `version` |
| `jec_site_expiry_timestamp_seconds` | When the certificate a [watched site](../usage/sites.md) served last ends; labels `host` and `port` |
| `jec_site_state` | 1 for the state a watched site is in (label `state`), 0 for the others |
| `jec_domain_expiry_timestamp_seconds` | When a [domain registration](../usage/sites.md#domain-registrations) ends; label `domain` |
| `jec_domain_state` | 1 for the state a domain is in (label `state`), 0 for the others |
| `jec_ct_unknown_certificates` | While [certificate transparency](../usage/sites.md#certificate-transparency) is on: the certificates found for a domain that JEC does not hold and nobody trusted or ignored; label `domain` |
| `jec_job_failed` | 1 when the last run of a background job (label `job`) failed, else 0 |
| `jec_data_free_bytes` | The bytes free on the file system of the data directory |
| `jec_data_size_bytes` | The size of that file system, in bytes |

Alerting rules, ready for a rules file of Prometheus:

```yaml
groups:
  - name: jec
    rules:
      - alert: CertificateEndsSoon
        expr: jec_certificate_expiry_timestamp_seconds - time() < 14 * 86400
        labels:
          severity: warning
        annotations:
          summary: "{{ $labels.domain }} ends in under 14 days"
      - alert: CertificateServedOld
        expr: jec_certificate_served_old > 0
        for: 1h
        annotations:
          summary: "{{ $labels.domain }} still serves an older certificate"
      - alert: CertificateChainIncomplete
        expr: jec_certificate_chain_incomplete > 0
        annotations:
          summary: "{{ $labels.domain }} serves no intermediates"
      - alert: ProviderFailing
        expr: jec_provider_test_ok == 0
        annotations:
          summary: "Provider {{ $labels.name }} fails its test"
      - alert: DomainEndsSoon
        expr: jec_domain_expiry_timestamp_seconds - time() < 30 * 86400
        annotations:
          summary: "The registration of {{ $labels.domain }} ends in under 30 days"
      - alert: JecJobFailing
        expr: jec_job_failed > 0
        for: 6h
        annotations:
          summary: "The JEC job {{ $labels.job }} keeps failing"
```

## Heartbeat

With the address a monitoring service gives, such as healthchecks.io or a
push monitor of Uptime Kuma, under Heartbeat in Data and backups on the
Settings page, JEC asks for it with GET every five minutes while it runs;
when JEC stops, or cannot reach it, the service alerts as it is set to. A
failed request counts as a failure of the background job
`kernel.heartbeat`, which marks the Settings link.

## Health

Without Prometheus, Background jobs and Free space for data under About
on the Settings page show the same: the background jobs (renewals,
checks, notes and so on) whose last run failed, with why, and the space
left on the file system of the data directory, in the warning color below
1 GB or 5%. The API is `GET /api/v1/system/health`, which a token that may
only read can read.
