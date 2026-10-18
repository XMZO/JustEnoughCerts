# 插件

[English](plugins.md)

插件是用任何语言写的程序，不用重新编译 JEC，就能给它加上更多的部署目标、通知渠道和域名验证方式。插件说明自己提供哪些类型，包括表单和各种语言的文字；JEC 像显示自己的类型一样显示它们，每次要做什么就运行一次插件。

## 安装插件

- 把程序放进 JEC 数据目录下的 `plugins` 目录：容器里是 `/data/plugins`（也就是 `docker-compose.yml` 旁边的 `./data/plugins`），其他情况是 `<数据目录>/plugins`。然后重启 JEC：它在启动时读取插件。
- 文件名去掉扩展名就是插件的 ID：只能用小写字母、数字、`-` 和 `_`，以字母开头，最多 32 个字符，比如 `acme-cdn` 或 `my_dns.py`。
- 文件要有执行权限。文件和目录都只能由属主写入，属主必须是 root 或运行 JEC 的用户（容器里是 65532）。其他文件 JEC 不运行。
- JEC 的容器镜像里只有 JEC，所以容器里的插件必须是静态链接的程序，比如 Go、Rust 编译出来的。在机器上直接运行 JEC 的二进制文件时，带 `#!` 的脚本也可以。
- “模块”页列出 JEC 找到的插件、每个插件加上的类型，或者没有启用的原因。关掉“插件”模块会隐藏插件的类型，但 JEC 启动时仍会让每个插件说明自己：要完全不运行某个插件，就把它删掉。

插件以 JEC 的权限运行，拿得到设置里的密钥：只安装你信任的插件。

## JEC 怎样运行插件

每个动作运行插件一次，不带参数。

- JEC 往插件的标准输入写一个 JSON 请求，然后关闭标准输入。
- 插件往标准输出写一个 JSON 回答（最多 1 MiB），然后退出。插件写到标准错误的内容，在动作失败时记进日志。
- 插件在自己的目录 `<plugins>/<ID>.data` 里运行，可以在那里存文件。环境变量是 JEC 的环境去掉 JEC 自己的 `JEC_` 开头的设置，再加上 `JEC_PLUGIN`（插件 ID）、`JEC_PLUGIN_DATA`（上面那个目录）和 `JEC_PLUGIN_PROTOCOL`（`1`）。
- 说明自己最多 10 秒，测试最多 1 分钟，其他动作最多 5 分钟；调用方的时间更短时以调用方为准，比如发通知是 30 秒。超时后 JEC 结束插件和它启动的进程。

每个请求都有 `"protocol": 1` 和 `action`；每个回答都必须有 `"protocol": 1` 和 `ok`。

## describe（说明自己）

JEC 启动时向每个插件发送 `{"protocol": 1, "action": "describe"}`。回答写明插件的名字和它提供的类型：

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

- 文字可以是一个字符串（所有语言都用它），也可以是按语言标签给出的对象。JEC 依次选用：用户语言的文字、同一语言其他地区的文字、所有语言通用的字符串、英文、第一个。文字原样显示，不解析任何标记。
- `kind` 是 `target`（部署证书）、`notifier`（发通知）或 `solver`（验证域名）。验证方式的 `challenge` 是 `dns`（默认，添加 TXT 记录）或 `http`（在网站上放文件）。DNS 验证可以用秒数说明记录多久才能查到、多久查一次：`"wait": {"timeout": 300, "interval": 10}`，不写时是 120 和 5。标了 `"records": true` 的 DNS 验证还能添加任意类型的记录，也就是有些证书厂商验证域名用的 TXT、CNAME 记录，Namecheap、阿里云等证书来源就能用它。
- 字段要有 `key`（字母、数字和 `_`，以字母开头）和 `kind`：`text`、`multiline`、`number`、`url`、`email`、`select` 或 `switch`（`"true"` 或 `"false"`）。可以有 `label`、写在下面的 `help`、`default`、选择框的 `options`，也可以是 `required`（必填）、`secret`（加密保存，保存后不再显示）、`advanced`（在表单里单独放）或者只在前面某个普通字段等于某个值时出现（`when`）。
- 通知渠道还会自动加上 JEC 自己的字段：消息语言和发送哪些事件。JEC 按选的语言写好通知再交给插件。
- 一个插件最多 32 种类型，每种最多 64 个字段。类型 ID 的规则和插件 ID 一样；JEC 里类型的全名是 `plugins.<插件>.<类型>`。类型和字段的 ID 用上以后不要再改：用户保存的设置引用着它们。

## 动作

其他请求都带 `type`（类型的 ID），以及配置好的实例的设置：普通设置 `config` 和密钥 `secrets`，都按字段的 key 给出。

| 动作 | 请求里还有 | 回答里可以有 |
|---|---|---|
| `test` | | `message`，插件检查的结果，显示给用户 |
| `deploy` | `certificate`、`previous` | `ref`、`removed` |
| `notify` | `note` | |
| `dns_present`、`dns_cleanup` | `record` | |
| `dns_add_record`、`dns_remove_record` | `dns_record`，删除时还有 `ref` | 添加时可以有 `ref` |
| `file_put`、`file_remove` | `file` | |

- `test` 只发给标了 `testable` 的类型，由卡片上的“测试连接”按钮触发。它只检查设置，不改动任何东西。
- `deploy` 安装一个新版本。`certificate` 里有 `id`、`domains`（主域名在前）、`serial`（十六进制）、`fingerprint`（叶子证书的 SHA-256，十六进制）、`not_before` 和 `not_after`（RFC 3339），以及 PEM 格式的 `cert`、`chain`、`fullchain`、`privkey`，和 certbot 的文件同名。`previous` 是这张证书以前部署到这个实例时回答的 `ref`，新的在前。回答里的 `ref` 说明目标现在放着什么（比如 CDN 上的证书 ID），下次会出现在 `previous` 里；`removed` 列出 `previous` 里被插件删掉的那些。
- `notify` 发送 `note`：`event`（比如 `certificate.issue_failed`，测试时是 `notify.test`）、`title`、`subject`（标题前加上 JEC 标记，适合很多来源混在一起的地方）、`text`、`link`（JEC 里对应的页面，JEC 知道自己的地址时才有）、`time` 和 `data`（事件自己的数据）。
- `dns_present` 添加、`dns_cleanup` 删除 DNS-01 验证的 TXT 记录：`record` 里有 `domain`、记录名 `fqdn`（比如 `_acme-challenge.example.com.`）和 `value`。
- `dns_add_record` 添加、`dns_remove_record` 删除任意类型的记录，只发给标了 `records` 的验证方式：`dns_record` 里有完整的记录名 `name`（比如 `_dnsauth.example.com`，末尾没有点）、类型 `type`（`TXT` 或 `CNAME`）和 `value`。`dns_add_record` 的回答里可以有 `ref`（比如这条记录在服务商那里的 ID），`dns_remove_record` 时原样放在 `ref` 里交还。
- `file_put` 放、`file_remove` 删网站要在 `http://<domain><path>` 提供的文件：`file` 里有 `domain`、`path`（在 `/.well-known/` 下）和 `content`。

## 回答

- 成功：`{"protocol": 1, "ok": true}`，加上动作需要的内容。
- 失败：`{"protocol": 1, "ok": false, "error": "出了什么问题", "retry": true, "retry_after": 60}`。JEC 原样显示 `error`。带 `retry` 时 JEC 稍后再试：给了 `retry_after` 就等这么多秒，否则等待时间逐渐拉长。不带时 JEC 不再重试，并告诉用户。
- 回答可以给所在的部署日志加几行，最多 100 行：`"log": [{"level": "ok", "text": "已上传"}]`，`level` 是 `info`、`ok`、`warn` 或 `error`。
- 插件没有回答或者超时，算作值得重试的失败；回答 JEC 看不懂则不重试。

## 示例

一个 Python 写的插件，适合在机器上直接运行 JEC 的二进制文件时使用：把证书和私钥写到一个目录的部署目标。

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

把它存成 JEC 数据目录下 `plugins` 目录里的 `copy.py`，用 `chmod 755` 加上执行权限，然后重启 JEC：部署目标里就会出现这个类型，在“插件”分组下。
