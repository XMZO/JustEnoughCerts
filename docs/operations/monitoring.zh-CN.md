# 用 Prometheus 监控

[English](monitoring.md)

JEC 在 `/api/v1/metrics/prometheus` 以 Prometheus 指标提供证书的情况，便于与其他服务一起做仪表盘和告警。指标由“Prometheus 指标”模块提供，可在“模块”页停用。

## 采集

1. 在设置页的“API 令牌”中创建一个只读令牌。
2. 在 Prometheus 中添加采集任务：

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

面板部署在子路径下时，路径以该子路径开头，例如 `/certs/api/v1/metrics/prometheus`。

## 指标

证书的每个指标都带 `id` 与 `domain`（主域名）两个标签。

| 指标 | 含义 |
|---|---|
| `jec_certificate_expiry_timestamp_seconds` | 当前版本的到期时间（Unix 秒） |
| `jec_certificate_renewal_timestamp_seconds` | 下一次续期的开始时间 |
| `jec_certificate_state` | 证书所处状态（标签 `state`）为 1，其余状态为 0 |
| `jec_certificate_targets` | 证书的部署目标数 |
| `jec_certificate_deployments_failed` | 最近一次部署失败的部署目标数 |
| `jec_certificate_served_old` | 仍在提供旧证书的域名数（需启用线上巡检模块） |
| `jec_certificate_unreachable` | 443 端口无 TLS 响应的域名数（同上） |
| `jec_certificates` | 各状态的证书数 |
| `jec_build_info` | JEC 的版本（标签 `version`） |
| `jec_site_expiry_timestamp_seconds` | [监控的站点](../usage/sites.zh-CN.md)上次所见证书的到期时间，标签为 `host` 与 `port` |
| `jec_site_state` | 监控站点所处状态（标签 `state`）为 1，其余为 0 |
| `jec_domain_expiry_timestamp_seconds` | [域名注册](../usage/sites.zh-CN.md#域名注册)的到期时间，标签为 `domain` |
| `jec_domain_state` | 域名所处状态（标签 `state`）为 1，其余为 0 |

证书在 14 天内到期时告警的示例：

```yaml
- alert: CertificateEndsSoon
  expr: jec_certificate_expiry_timestamp_seconds - time() < 14 * 86400
  labels:
    severity: warning
  annotations:
    summary: "{{ $labels.domain }} 将在 14 天内到期"
```
