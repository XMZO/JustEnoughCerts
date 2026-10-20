# 配置导入导出

[English](config-file.md)

在设置页的“数据和备份 → 配置文件”中，可将凭据、服务商、证书、续期默认值与时区导出为 YAML 文件，或导入此类文件，用于将配置纳入 Git 管理或迁移至另一实例。该文件与服务器的运行配置 `config.yaml` 无关。

## 文件格式

证书按名称引用服务商：

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

| 部分 | 内容 |
|---|---|
| `version` | 文件格式版本，当前为 1 |
| `credentials` | 凭据：名称、种类、普通设置（`config`），导出含密钥时另有密钥（`secrets`） |
| `providers` | 服务商：名称、类型、所用凭据的名称（`credential`）、普通设置（`config`），导出含密钥时另有密钥（`secrets`） |
| `time_zone` | 面板的时区；跟随服务器时不导出，导入空字符串则改回跟随服务器 |
| `renewal` | 续期默认值：提前天数、每日检查时间、默认密钥类型与重试次数 |
| `certificates` | 证书：域名、证书来源、验证方式、备用来源（`backup_source`）、密钥类型、续期天数与部署目标 |

导出的文件不含导出时间等每次变化的内容，纳入 Git 后的差异即为实际改动。

## 导入规则

- 新增 JEC 中不存在的条目，更新已存在的条目：服务商按种类与名称匹配，证书按域名匹配。
- 不删除任何条目。文件中未写明的密钥保持不变（新增的服务商须在文件中提供密钥），已有证书的来源不会变更。
- 导入前列出全部变更；任一条目有错误时，不做任何修改。
- 文件包含无法识别的键（含大小写错误），或格式版本高于当前程序时，整个文件被拒绝。
- 模块停用时，对应部分被跳过并给出提示。

## 导出

导出默认不含密钥；导出含密钥的文件须输入登录密码，该操作记入审计日志。

## 接口

| 接口 | 用途 |
|---|---|
| `GET /api/v1/config` | 导出（不含密钥），只读令牌即可调用，适合定时导出至 Git |
| `POST /api/v1/config/export` | 导出含密钥的文件，须提供密码 |
| `POST /api/v1/config/check` | 预览文件将做的改动 |
| `POST /api/v1/config/import` | 导入 |
