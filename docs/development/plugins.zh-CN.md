# 插件

[English](plugins.md)

插件是可用任意语言编写的独立程序，用于在不重新编译 JEC 的情况下新增证书来源、部署目标、通知渠道与域名验证方式。插件声明其提供的类型（包括表单字段与多语言文字），JEC 以与内置类型相同的方式展示这些类型，并在每次执行操作时运行插件。

## 安装

- 将程序放入 JEC 数据目录下的 `plugins` 目录：容器中为 `/data/plugins`（即 `docker-compose.yml` 同级目录下的 `./data/plugins`），其他情况下为 `<数据目录>/plugins`。JEC 在启动时读取插件，因此放入后须重启 JEC。
- 插件 ID 为去掉扩展名的文件名，仅可包含小写字母、数字、`-` 与 `_`，须以字母开头，长度不超过 32 个字符，例如 `acme-cdn` 或 `my_dns.py`。
- 文件须具有执行权限。文件及其所在目录仅允许属主写入，属主须为 root 或运行 JEC 的用户（容器中为 65532）；不满足条件的文件不会被运行。
- JEC 的容器镜像中仅包含 JEC 本身，因此容器中的插件须为静态链接的程序，例如由 Go、Rust 编译的程序。直接在主机上运行 JEC 的二进制文件时，也可使用带 `#!` 行的脚本。
- “模块”页列出 JEC 发现的插件、各插件新增的类型，以及插件未启用的原因。停用“插件”模块会隐藏插件提供的类型，但 JEC 启动时仍会请求每个插件声明自身；如需完全禁止某个插件运行，请将其删除。

插件以 JEC 的权限运行，并可获得其设置中的密钥，请仅安装可信的插件。

## 运行方式

每次操作运行插件一次，不传递命令行参数。

- JEC 向插件的标准输入写入一个 JSON 请求，然后关闭标准输入。
- 插件向标准输出写入一个 JSON 应答（不超过 1 MiB）后退出。插件写入标准错误的内容在操作失败时记入日志。
- 插件的工作目录为 `<plugins>/<ID>.data`，可在其中保存文件。环境变量为 JEC 的环境变量去除 JEC 自身以 `JEC_` 开头的设置，并增加 `JEC_PLUGIN`（插件 ID）、`JEC_PLUGIN_DATA`（上述工作目录）与 `JEC_PLUGIN_PROTOCOL`（`1`）。
- 时限：声明自身 10 秒，测试 1 分钟，其他操作 5 分钟；调用方的时限更短时以调用方为准，例如发送通知为 30 秒。超时后，JEC 终止插件及其启动的进程。

每个请求均包含 `"protocol": 1` 与 `action`；每个应答均须包含 `"protocol": 1` 与 `ok`。

## describe：声明类型

JEC 启动时向每个插件发送 `{"protocol": 1, "action": "describe"}`。应答须包含插件的名称及其提供的类型：

```json
{
  "protocol": 1,
  "ok": true,
  "name": {"en-US": "Acme CDN", "zh-CN": "Acme CDN 加速"},
  "description": "把证书部署到 Acme CDN。",
  "types": [
    {
      "id": "cdn",
      "kind": "target",
      "testable": true,
      "name": {"en-US": "Acme CDN", "zh-CN": "Acme CDN 加速"},
      "description": {"en-US": "Uploads each new certificate.", "zh-CN": "上传每张新证书，再删掉被替换的旧证书。"},
      "fields": [
        {"key": "api_token", "kind": "text", "label": "API Token", "secret": true, "required": true},
        {"key": "region", "kind": "select", "label": {"en-US": "Region", "zh-CN": "区域"}, "default": "eu",
         "options": [{"value": "eu", "label": "Europe"}, {"value": "us", "label": "America"}]},
        {"key": "bucket", "kind": "text", "label": "Bucket", "advanced": true,
         "when": {"key": "region", "value": "eu"}}
      ]
    }
  ]
}
```

- 文字可以是字符串（适用于所有语言），也可以是以语言标签为键的对象。JEC 按以下顺序选用：用户语言的文字、同一语言其他地区的文字、通用字符串、英文、第一项。文字按原样显示，不解析任何标记。
- `kind` 的取值：`source`（签发或取回证书）、`target`（部署证书）、`notifier`（发送通知）、`solver`（验证域名）。
- 验证方式的 `challenge` 为 `dns`（默认，添加 TXT 记录）或 `http`（在网站上放置文件）。DNS 验证方式可声明记录生效的超时时间与查询间隔（单位为秒）：`"wait": {"timeout": 300, "interval": 10}`，省略时分别为 120 与 5。声明 `"records": true` 的 DNS 验证方式还可添加任意类型的记录，即部分证书厂商用于验证域名的 TXT、CNAME 记录，可供 Namecheap、阿里云等证书来源使用。
- 证书来源依据 CSR 签发证书，私钥由 JEC 生成。如其 CA 通过网站文件或 DNS 记录验证域名，须在 `challenges` 中声明 `http`、`record` 或两者；使用该来源的证书须选择能够满足要求的验证方式，JEC 按插件的要求放置文件或记录（参见 `issue`）。声明 `"own_key": true` 的来源提供自带私钥的现成证书，例如取自证书仓库或其他工具，不经 JEC 验证域名。声明 `"revocable": true` 的来源可以吊销自己提供的证书（参见 `revoke`）。
- 字段须包含 `key`（字母、数字与 `_`，以字母开头）与 `kind`：`text`、`multiline`、`number`、`url`、`email`、`select` 或 `switch`（取值为 `"true"` 或 `"false"`）。可选属性：`label`、显示在字段下方的 `help`、`default`、选择框的 `options`、`required`（必填）、`secret`（加密保存，保存后不再显示）、`advanced`（在表单中归入高级设置），以及 `when`（仅当前面某个普通字段等于指定值时显示）。
- 通知渠道自动附加 JEC 的公共字段：消息语言与要发送的事件。JEC 按所选语言生成通知内容后交给插件。
- 每个插件最多提供 32 种类型，每种类型最多 64 个字段。类型 ID 的规则与插件 ID 相同；类型在 JEC 中的完整名称为 `plugins.<插件>.<类型>`。类型与字段的 ID 一经使用不应再修改，因为用户保存的设置引用这些 ID。

## 操作

除 `describe` 外，每个请求均包含 `type`（类型 ID）以及所配置实例的设置：普通设置 `config` 与密钥 `secrets`，均以字段的 key 为键。

| 操作 | 请求的附加内容 | 应答可包含的内容 |
|---|---|---|
| `test` | | `message`：插件的检查结果，显示给用户 |
| `deploy` | `certificate`、`previous` | `ref`、`removed` |
| `notify` | `note` | |
| `dns_present`、`dns_cleanup` | `record` | |
| `dns_add_record`、`dns_remove_record` | `dns_record`；删除时另含 `ref` | 添加时可含 `ref` |
| `file_put`、`file_remove` | `file` | |
| `issue` | `issue` | `chain` 或 `wait` |
| `collect` | `collect` | `chain` 与 `key`，或 `wait` |
| `revoke` | `revoke` | |

- `test`：仅发送给声明了 `testable` 的类型，由卡片上的“测试连接”按钮触发；只检查设置，不做任何修改。
- `deploy`：安装新版本。`certificate` 包含 `id`、`domains`（主域名在前）、`serial`（十六进制）、`fingerprint`（叶子证书的 SHA-256，十六进制）、`not_before` 与 `not_after`（RFC 3339），以及 PEM 格式的 `cert`、`chain`、`fullchain`、`privkey`（与 certbot 的文件同名）。`previous` 为该证书此前部署到此实例时应答的 `ref`，按时间倒序排列。应答中的 `ref` 标识目标当前保存的内容（例如 CDN 上的证书 ID），下次部署时出现在 `previous` 中；`removed` 列出插件已删除的 `previous` 条目。
- `notify`：发送 `note`，其中包含 `event`（例如 `certificate.issue_failed`，测试时为 `notify.test`）、`title`、`subject`（带 JEC 标记的标题，适用于多个来源混合的场合）、`text`、`link`（JEC 中对应的页面，仅在 JEC 知道自身地址时提供）、`time` 与 `data`（事件自身的数据）。
- `dns_present`、`dns_cleanup`：分别添加、删除 DNS-01 验证的 TXT 记录。`record` 包含 `domain`、记录名 `fqdn`（例如 `_acme-challenge.example.com.`）与 `value`。
- `dns_add_record`、`dns_remove_record`：分别添加、删除任意类型的记录，仅发送给声明了 `records` 的验证方式。`dns_record` 包含完整记录名 `name`（例如 `_dnsauth.example.com`，末尾无点）、类型 `type`（`TXT` 或 `CNAME`）与 `value`。`dns_add_record` 的应答可包含 `ref`（例如该记录在服务商处的 ID），`dns_remove_record` 时原样作为 `ref` 传回。
- `file_put`、`file_remove`：分别放置、删除网站须在 `http://<domain><path>` 提供的文件。`file` 包含 `domain`、`path`（位于 `/.well-known/` 下）与 `content`。
- `issue`：向证书来源请求证书。`issue` 包含 `domains`（主域名在前，通配符写作 `*.example.com`）与 PEM 格式的 `csr`；CSR 由 JEC 为本次签发生成的私钥签署，主题为主域名，名称列表包含全部域名。同一次签发的每次尝试使用同一私钥，因此插件可继续使用此前创建的订单。等待之后的请求中，`issue` 另含该次等待返回的 `state`、JEC 已放置的 `files`（`domain` 与 `path`）以及已添加的 `records`（`name`、`type`、`value` 与 `visible`；`visible` 表示域名所在区域的权威 DNS 服务器是否已可查到该记录）。应答包含 PEM 格式的 `chain`（叶子证书在前，对应 CSR 的私钥，覆盖全部域名），或 `wait`（参见下文）。
- `collect`：向声明了 `own_key` 的来源请求比当前所用证书更新的证书。`collect` 包含 `domains`；已有证书在用时，另含 `current`（叶子证书的 SHA-256，十六进制）与 `current_end`（到期时间，RFC 3339）；等待之后的请求另含 `state`。应答包含 PEM 格式的 `chain` 与证书私钥 `key`；没有更新的证书时应答 `wait`。
- `revoke`：仅发送给声明了 `revocable` 的来源，在用户吊销该来源提供的证书版本时发送。`revoke` 包含 PEM 格式的 `certificate`（叶子证书）、`serial`（序列号，十六进制）、`fingerprint`（SHA-256，十六进制）与 `reason`（原因）：`unspecified`、`key_compromise`、`superseded` 或 `cessation_of_operation`。证书已被吊销不算错误。应答失败时，其 `error` 显示给用户。

## 等待

证书尚未就绪时，来源应答 `"wait": {"after": 300, "state": "..."}`。JEC 在 `after` 秒（1 分钟至 1 天）后再次请求，并原样传回 `state`，其内容由插件自行定义。等待期间，签发显示为“等待中”而非失败；何时放弃由插件决定，放弃时应答失败。

签发证书的来源可在等待中声明验证所需的 `files`（`domain`、`/.well-known/` 下的 `path` 与 `content`）与 `records`（`domain`、`name`、`type`（`TXT` 或 `CNAME`）与 `value`）。JEC 在等待前通过证书所选的验证方式放置这些文件与记录：后续等待中仍列出的予以保留，不再列出的予以删除；插件应答证书后，删除其余全部。每一项的 `domain` 须为证书的域名之一；记录的 `name` 为完整记录名，或如控制台所示相对于该域名的名称（`_dnsauth` 表示 `_dnsauth.example.com`）。JEC 无法放置时，本次尝试失败，下一次尝试将收到本次等待的 `state`。

以下示例为使用 DNS 验证的 CA 的证书来源。请求：

```json
{"protocol": 1, "action": "issue", "type": "dv", "config": {}, "secrets": {"api_key": "..."},
 "issue": {"domains": ["example.com", "www.example.com"], "csr": "-----BEGIN CERTIFICATE REQUEST-----..."}}
```

插件创建订单，并应答 CA 须检查的记录：

```json
{"protocol": 1, "ok": true, "wait": {"after": 300, "state": "order-12345",
 "records": [{"domain": "example.com", "name": "_dnsauth", "type": "TXT", "value": "abc123"},
             {"domain": "www.example.com", "name": "_dnsauth", "type": "TXT", "value": "def456"}]}}
```

5 分钟后，JEC 再次请求：

```json
{"protocol": 1, "action": "issue", "type": "dv", "config": {}, "secrets": {"api_key": "..."},
 "issue": {"domains": ["example.com", "www.example.com"], "csr": "-----BEGIN CERTIFICATE REQUEST-----...",
  "state": "order-12345",
  "records": [{"name": "_dnsauth.example.com", "type": "TXT", "value": "abc123", "visible": true},
              {"name": "_dnsauth.www.example.com", "type": "TXT", "value": "def456", "visible": true}]}}
```

插件请求 CA 检查（如尚未请求），并在 CA 签发之前持续应答相同的等待；签发后应答 `{"protocol": 1, "ok": true, "chain": "-----BEGIN CERTIFICATE-----..."}`，JEC 随即删除这些记录。

## 应答

- 成功：`{"protocol": 1, "ok": true}`，并附加操作所需的内容。
- 失败：`{"protocol": 1, "ok": false, "error": "出了什么问题", "retry": true, "retry_after": 60}`。JEC 原样显示 `error`。包含 `retry` 时，JEC 稍后重试：提供 `retry_after` 时等待相应秒数，否则采用逐渐延长的间隔；不包含时，JEC 不再重试并告知用户。
- 应答可向所属部署或签发的日志追加至多 100 行：`"log": [{"level": "ok", "text": "已上传"}]`，`level` 取值为 `info`、`ok`、`warn` 或 `error`。
- 插件未应答或超时，视为可重试的失败；应答无法解析时不重试。

## 示例

以下 Python 插件实现一个部署目标，将证书与私钥写入指定目录，适用于直接在主机上运行 JEC 二进制文件的场景。

```python
#!/usr/bin/env python3
import json
import os
import sys

req = json.load(sys.stdin)

def answer(**fields):
    json.dump({"protocol": 1, **fields}, sys.stdout)

if req["action"] == "describe":
    answer(ok=True, name="Example", types=[{
        "id": "copy", "kind": "target", "testable": True,
        "name": {"en-US": "Copy to a directory", "zh-CN": "复制到目录"},
        "fields": [{"key": "dir", "kind": "text", "required": True,
                    "label": {"en-US": "Directory", "zh-CN": "目录"}}],
    }])
elif req["action"] == "test":
    d = req["config"]["dir"]
    if os.path.isdir(d):
        answer(ok=True, message=f"{d} 存在")
    else:
        answer(ok=False, error=f"没有目录 {d}")
elif req["action"] == "deploy":
    d, cert = req["config"]["dir"], req["certificate"]
    for name in ("fullchain", "privkey"):
        path = os.path.join(d, name + ".pem")
        with open(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
            f.write(cert[name])
    answer(ok=True, log=[{"level": "ok", "text": f"已写入 {d}"}])
else:
    answer(ok=False, error="不认识的动作 " + req["action"])
```

将其保存为 JEC 数据目录下 `plugins` 目录中的 `copy.py`，执行 `chmod 755` 赋予执行权限，然后重启 JEC，部署目标的“插件”分组中即会出现该类型。
