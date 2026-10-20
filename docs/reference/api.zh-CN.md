# API

[English](api.md)

面板的全部功能均通过 HTTP API 提供，位于 `<面板地址>/api/v1`。接口说明以 OpenAPI 文档的形式提供于 `/api/v1/openapi.json`，读取该文档无需登录。

## 认证

| 方式 | 说明 |
|---|---|
| 会话 Cookie | 浏览器登录后使用；其他网站发出的修改类请求会被拒绝（CSRF 防护） |
| API 令牌 | 供脚本与其他程序使用：在设置页的“API 令牌”中创建，以 `Authorization: Bearer jec_...` 发送 |

API 令牌分为只读与读写两种，可设有效期，到期后返回 401。管理账号（密码、两步验证、登录有效期、API 令牌与单点登录）与恢复备份只能在登录后进行，令牌无法使用。经令牌执行的修改在审计日志中注明令牌名称。

## 示例

列出全部证书：

```sh
curl -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/certs/certificates
```

立即签发编号为 1 的证书：

```sh
curl -X POST -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/certs/certificates/1/issue
```

导出配置文件（不含密钥），可用于定时备份至 Git：

```sh
curl -H "Authorization: Bearer jec_..." https://certs.example.com/api/v1/config > jec.yaml
```

## 错误

请求失败时返回相应的 HTTP 状态码与如下 JSON：

```json
{"code": "kernel.error.invalid_request", "params": {}, "fields": [{"location": "body.domains", "code": "kernel.error.field_required"}]}
```

`code` 为消息键，`params` 为其参数，`fields` 指出出错的字段。消息键对应的文字见语言文件 `i18n/locales/`。

## 健康检查

`GET /healthz` 在服务正常时返回 200，无需登录；容器镜像的健康检查使用 `jec healthcheck` 访问该地址。
