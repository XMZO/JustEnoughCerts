# 翻译 JEC

[English](translating.md)

JEC 显示的每一句话，包括网页界面、错误信息、签发日志和通知，都放在 [`i18n/locales/`](../i18n/locales) 下每种语言一个的 JSON 文件里。服务端把这些文件编进程序，网页界面读的也是同一批文件，所以在这里加一种语言，各处就都有了。美式英语（`en-US.json`）是基准：它定义全部的 key，别的语言缺的句子都用它的。

## 加一种语言

1. 把 `i18n/locales/en-US.json` 复制一份，按这种语言的 [BCP 47 标签](https://www.w3.org/International/articles/language-tags/) 命名，比如 `de-DE.json`、`pt-BR.json`。
2. 填好 `_meta`：`name` 是这种语言用自己写的名字（`Deutsch`），`dir` 是 `ltr`，阿拉伯语、希伯来语等写 `rtl`。`completeness` 不用管，工具会维护。
3. 翻译各个值。key 和花括号里的名字保持原样。
4. 运行 `go run ./tools/i18n fix` 和 `go generate ./i18n`（见下），后者会把这种语言加进网页界面的语言列表。之后设置页的语言菜单里就有它，通知渠道也能选它。

不必一次翻完：没翻的句子显示英文，`completeness` 会写明完成了多少。只有 `en-US` 和 `zh-CN` 必须翻译全部 key，所以欢迎先交一份部分翻译。

## 工具

```sh
go run ./tools/i18n fix    # 排好顺序，写上 completeness
go run ./tools/i18n check  # CI 跑的检查
go generate ./i18n         # 加了或删了 key 之后
```

`check` 会报出：基准语言或 `zh-CN` 缺的 key，基准语言里没有的 key，空句子，新模块留下的 `TODO:` 占位，解析不了的句子，以及参数、复数分支或 select 分支和英文对不上的句子。每次改完都运行 `fix`：文件不是规范格式时 CI 会失败。

## 句子的语法

句子用的是 [ICU MessageFormat](https://unicode-org.github.io/icu/userguide/format_parse/messages/) 里服务端和网页界面（[FormatJS](https://formatjs.github.io/)）渲染结果一致的那部分：

| 形式 | 例子 |
|---|---|
| 参数 | `Renew {domain} now` |
| 复数 | `{count, plural, one {# certificate} other {# certificates}}` |
| 精确的数 | `{count, plural, =0 {no certificates} other {# certificates}}` |
| 选择 | `{kind, select, dns {DNS record} http {file} other {check}}` |

- 英文句子里的每个参数都要保留，名字和种类不变；位置可以挪，语言没有复数变化时也不必用复数形式。检查会核对这些。
- 复数分支里的 `#` 是那个数，按这种语言的习惯写出来。写这种语言有的复数类别（`zero`、`one`、`two`、`few`、`many`、`other`，见 [CLDR](https://www.unicode.org/cldr/charts/latest/supplemental/language_plural_rules.html)），`other` 一定要有。中文只写 `other` 即可。
- 单引号会把后面的 `{`、`}`、`<`、`>` 或 `#` 一直引到下一个单引号，所以 `'{{.domains}}'` 就是文字 `{{.domains}}`；两个单引号表示一个。其他单引号就是普通文字，`don't` 不用处理。
- 数字、日期、时间参数在进入句子之前就已经格式化好了：没有 `{n, number}`、`{d, date}` 这样的写法。

## 风格

- 用平实、简短的句子。界面很紧凑，手机上尤其如此：伪语言测试把每句话都拉长到英文的 140%，页面溢出就失败，所以两种说法都好时选短的。
- 名字保持原样：产品和服务的名字（Let's Encrypt、阿里云 ESA、DNSPod），协议和字段名（ACME、CNAME、`_acme-challenge`），命令、路径和环境变量。
- 同一个概念各处用同一个词。中文里：证书来源、验证方式、部署目标、通知渠道、签发、续期、部署。
- 错误信息说清楚发生了什么、接下来该怎么做，不责怪读者。

## 检查效果

```sh
pnpm -C web build && go build -o build/jec ./cmd/jec
JEC_E2E_BIN=../build/jec pnpm -C web e2e  # 伪语言测试，走遍每个页面
```

运行 JEC，在设置页切换语言，或者让它跟随浏览器。也用手机大小的窗口看看：底部标签栏、证书列表和各个表单最先暴露过长的词。

## 插件

插件在它的类型里自带各语言的文字（[docs/plugins.zh-CN.md](plugins.zh-CN.md)）；它不认识的语言和 JEC 自己的一样，退回英文。
