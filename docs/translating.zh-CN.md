# 翻译指南

[English](translating.md)

JEC 显示的全部文字，包括网页界面、错误信息、签发日志与通知，均存放在 [`i18n/locales/`](../i18n/locales) 下的 JSON 文件中，每种语言一个文件。服务端将这些文件编译进程序，网页界面也读取同一组文件，因此新增一种语言即可在各处生效。美式英语（`en-US.json`）为基准语言：它定义全部 key，其他语言缺失的文字均以英文代替。

## 新增语言

1. 复制 `i18n/locales/en-US.json`，按该语言的 [BCP 47 标签](https://www.w3.org/International/articles/language-tags/)命名，例如 `de-DE.json`、`pt-BR.json`。
2. 填写 `_meta`：`name` 为该语言自身的名称（例如 `Deutsch`）；`dir` 为 `ltr`，阿拉伯语、希伯来语等从右向左书写的语言为 `rtl`。`completeness` 由工具维护，无需填写。
3. 翻译各项的值；key 与花括号中的参数名保持不变。
4. 运行 `go run ./tools/i18n fix` 与 `go generate ./i18n`（参见下文）。后者将该语言加入网页界面的语言列表，此后即可在设置页的语言菜单与通知渠道的语言选项中选择该语言。

翻译无需一次完成：未翻译的文字显示为英文，`completeness` 记录完成比例。仅 `en-US` 与 `zh-CN` 须包含全部 key，因此欢迎先提交部分翻译。

## 工具

```sh
go run ./tools/i18n fix    # 规范排序，并写入 completeness
go run ./tools/i18n check  # CI 执行的检查
go generate ./i18n         # 新增或删除 key 之后运行
```

`check` 报告以下问题：基准语言或 `zh-CN` 缺失的 key、基准语言中不存在的 key、空文字、新模块遗留的 `TODO:` 占位、无法解析的文字，以及参数、复数分支或选择分支与英文不一致的文字。每次修改后均须运行 `fix`：文件不符合规范格式时 CI 将失败。

## 消息语法

消息采用 [ICU MessageFormat](https://unicode-org.github.io/icu/userguide/format_parse/messages/) 中服务端与网页界面（[FormatJS](https://formatjs.github.io/)）渲染结果一致的子集：

| 形式 | 示例 |
|---|---|
| 参数 | `Renew {domain} now` |
| 复数 | `{count, plural, one {# certificate} other {# certificates}}` |
| 精确数值 | `{count, plural, =0 {no certificates} other {# certificates}}` |
| 选择 | `{kind, select, dns {DNS record} http {file} other {check}}` |

- 须保留英文消息中的全部参数，名称与类型保持一致；参数位置可以调整，没有复数变化的语言无需使用复数形式。检查工具会核对上述各项。
- 复数分支中的 `#` 表示该数值，按该语言的习惯书写。请提供该语言具有的复数类别（`zero`、`one`、`two`、`few`、`many`、`other`，参见 [CLDR](https://www.unicode.org/cldr/charts/latest/supplemental/language_plural_rules.html)），其中 `other` 为必需项；中文仅需 `other`。
- 单引号用于转义其后的 `{`、`}`、`<`、`>` 或 `#`，直至下一个单引号，因此 `'{{.domains}}'` 表示文字 `{{.domains}}`；两个连续的单引号表示一个单引号。其他位置的单引号按普通文字处理，`don't` 无需转义。
- 数字、日期与时间参数在传入消息前已完成格式化，不支持 `{n, number}`、`{d, date}` 等写法。

## 文风

- 使用简明的短句。界面布局紧凑，在手机上尤甚：伪语言测试将每段文字加长至英文的 140%，页面溢出即判定失败，因此在同样准确的前提下，请选择较短的表述。
- 名称保持原样：产品与服务名称（Let's Encrypt、阿里云 ESA、DNSPod）、协议与字段名（ACME、CNAME、`_acme-challenge`）、命令、路径与环境变量。
- 同一概念在各处使用同一术语。中文术语为：证书来源、验证方式、部署目标、通知渠道、签发、续期、部署。
- 错误信息应说明发生了什么以及下一步如何处理，不指责用户。

## 检查翻译效果

```sh
pnpm -C web build && go build -o build/jec ./cmd/jec
JEC_E2E_BIN=../build/jec pnpm -C web e2e  # 伪语言测试，遍历全部页面
```

运行 JEC，在设置页切换语言，或使其跟随浏览器语言。请同时在手机尺寸的窗口中检查：底部标签栏、证书列表与各类表单最容易暴露过长的文字。

## 插件

插件在其类型中自带各语言的文字，参见[插件](plugins.zh-CN.md)；对于插件未提供的语言，与 JEC 自身一样回退至英文。
