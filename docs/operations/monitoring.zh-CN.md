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
| `jec_provider_test_ok` | 服务商最近一次测试（每日自检或手动测试）通过为 1、失败为 0；设置改动后尚未测试的不列出；标签为 `id`、`name`、`kind` |
| `jec_certificate_chain_incomplete` | 提供当前版本但缺少中间证书的域名数（同上） |
| `jec_certificates` | 各状态的证书数 |
| `jec_build_info` | JEC 的版本（标签 `version`） |
| `jec_site_expiry_timestamp_seconds` | [监控的站点](../usage/sites.zh-CN.md)上次所见证书的到期时间，标签为 `host` 与 `port` |
| `jec_site_state` | 监控站点所处状态（标签 `state`）为 1，其余为 0 |
| `jec_domain_expiry_timestamp_seconds` | [域名注册](../usage/sites.zh-CN.md#域名注册)的到期时间，标签为 `domain` |
| `jec_domain_state` | 域名所处状态（标签 `state`）为 1，其余为 0 |
| `jec_ct_unknown_certificates` | 开启[证书透明度](../usage/transparency.zh-CN.md)时：某域名下发现的、不是 JEC 管理且未被信任或忽略的证书数，标签为 `domain` |
| `jec_job_failed` | 后台任务（标签 `job`）上次运行失败为 1，否则为 0 |
| `jec_data_free_bytes` | 数据目录所在文件系统的可用空间（字节） |
| `jec_data_size_bytes` | 数据目录所在文件系统的总大小（字节） |

告警规则示例（可直接放入 Prometheus 的规则文件）：

```yaml
groups:
  - name: jec
    rules:
      - alert: CertificateEndsSoon
        expr: jec_certificate_expiry_timestamp_seconds - time() < 14 * 86400
        labels:
          severity: warning
        annotations:
          summary: "{{ $labels.domain }} 将在 14 天内到期"
      - alert: CertificateServedOld
        expr: jec_certificate_served_old > 0
        for: 1h
        annotations:
          summary: "{{ $labels.domain }} 线上仍是旧证书"
      - alert: CertificateChainIncomplete
        expr: jec_certificate_chain_incomplete > 0
        annotations:
          summary: "{{ $labels.domain }} 缺少中间证书"
      - alert: ProviderFailing
        expr: jec_provider_test_ok == 0
        annotations:
          summary: "服务商 {{ $labels.name }} 测试失败"
      - alert: DomainEndsSoon
        expr: jec_domain_expiry_timestamp_seconds - time() < 30 * 86400
        annotations:
          summary: "域名 {{ $labels.domain }} 的注册将在 30 天内到期"
      - alert: JecJobFailing
        expr: jec_job_failed > 0
        for: 6h
        annotations:
          summary: "JEC 的后台任务 {{ $labels.job }} 持续失败"
```

## 心跳监控

设置页“数据和备份”中的“心跳监控”填写 healthchecks.io、Uptime Kuma 推送监控等服务给出的地址后，JEC 运行时每 5 分钟以 GET 访问一次；JEC 停止运行或无法访问时，由该服务按其设置报警。访问失败会记为后台任务 `kernel.heartbeat` 失败，并在设置入口显示警告。

## 运行状况

不用 Prometheus 时，设置页“运行环境”中的“后台任务”与“剩余空间”显示同样的信息：上次运行失败的后台任务（续期、检查、通知等）及其原因，以及数据目录所在文件系统的剩余空间（少于 1 GB 或 5% 时以警告色显示）。接口为 `GET /api/v1/system/health`，只读的 API 令牌即可读取。
