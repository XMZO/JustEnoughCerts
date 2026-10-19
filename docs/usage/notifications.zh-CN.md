# 通知

[English](notifications.md)

通知渠道在“通知”页管理。证书发生选定的事件时，JEC 按每个渠道所选的语言生成消息并发送。卡片上的“发送测试”用于立即发送一条测试消息。

## 事件

| 事件 | 默认 |
|---|---|
| 签发失败 | 开启 |
| 部署失败 | 开启 |
| 证书快到期或已经过期 | 开启 |
| 网站上还是旧证书（线上巡检发现） | 开启 |
| 有新证书（签发或上传） | 关闭 |
| 部署成功 | 关闭 |

每个渠道可单独选择事件与消息语言。消息标题以“【JEC】”开头，便于在群聊中识别；设置了 `JEC_BASE_URL` 时，消息附带对应页面的链接。同一事件不会重复发送到同一渠道；超过 3 天仍未送达的消息不再发送。

## 通知渠道

| 渠道 | 说明 |
|---|---|
| 电子邮件 | 经 SMTP 发送 |
| Telegram | 机器人令牌与会话 ID；可设置代理与自建 API 地址 |
| 钉钉、飞书（Lark）、企业微信 | 群机器人；关键词可设为 `JEC`；钉钉与飞书支持加签 |
| Server酱 | 按 SendKey 区分 Turbo 版与 Server酱³ |
| Bark | 默认使用官方服务器，也可使用自建服务器 |
| ntfy、Gotify | 可使用自建服务器 |
| Discord | 频道的 Webhook 地址 |
| Slack | Incoming Webhook，兼容 Mattermost 与 Rocket.Chat |
| Pushover | 用户密钥与应用令牌 |
| Webhook | 以 JSON 或自定义模板发送；可设置签名密钥、请求头、请求方法与 Content-Type |
| 运行脚本 | 由内置 shell 运行数据目录 `hooks/` 中的脚本，参见下文 |

可自建服务端的渠道（Bark、ntfy、Gotify、Slack、Webhook）提供“允许内网地址”开关，默认只连接公网地址。连接境外服务的渠道（Telegram、Discord、Slack、Pushover、ntfy、Gotify、Bark、Webhook）可在高级设置中单独设置代理。

## 运行脚本

“运行脚本”渠道用于对接其他系统：

- 脚本放在数据目录的 `hooks/` 中（首次使用时创建，仅属主可访问），由内置 shell（兼容 POSIX sh 与 Bash）运行，最长 20 秒。
- 消息以环境变量 `JEC_EVENT`、`JEC_TIME`、`JEC_TITLE`、`JEC_SUBJECT`、`JEC_TEXT`、`JEC_LINK` 提供；事件关于某张证书时另有 `JEC_CERTIFICATE_ID`、`JEC_DOMAIN`、`JEC_DOMAINS`、`JEC_NOT_AFTER`、`JEC_FINGERPRINT`。标准输入为整条消息的 JSON，格式与 Webhook 相同。
- 退出码 0 表示成功；75 表示暂时失败，稍后重试；其他退出码、语法错误与超时不再重试。

## 插件

插件可提供新的通知渠道，参见[插件](../development/plugins.zh-CN.md)。
