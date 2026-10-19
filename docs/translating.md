# Translation guide

[中文](translating.zh-CN.md)

Every text JEC shows, in the web interface, error messages, issuance logs
and notifications, is stored in one JSON file per language under
[`i18n/locales/`](../i18n/locales). The server embeds these files and the
web interface loads the same ones, so a language added there takes effect
everywhere at once. American English (`en-US.json`) is the base language:
it defines every key, and stands in for any message another language
leaves out.

## Adding a language

1. Copy `i18n/locales/en-US.json` to a file named after the language's
   [BCP 47 tag](https://www.w3.org/International/articles/language-tags/),
   such as `de-DE.json` or `pt-BR.json`.
2. Fill in `_meta`: `name` is the language's name in the language itself
   (such as `Deutsch`); `dir` is `ltr`, or `rtl` for right-to-left scripts
   such as Arabic and Hebrew. `completeness` is maintained by the tools.
3. Translate the values; keep the keys, and the argument names in braces,
   unchanged.
4. Run `go run ./tools/i18n fix` and `go generate ./i18n` (see below). The
   latter adds the language to the web interface's list, after which it can
   be chosen in the language menu of the Settings page and for notification
   channels.

A translation need not be complete: untranslated messages are shown in
English, and `completeness` records the progress. Only `en-US` and `zh-CN`
must contain every key, so partial translations are welcome as a start.

## Tools

```sh
go run ./tools/i18n fix    # sort the files and write completeness
go run ./tools/i18n check  # the checks CI runs
go generate ./i18n         # after adding or removing keys
```

`check` reports keys missing from the base language or from `zh-CN`, keys
absent from the base language, empty texts, the `TODO:` placeholders of
new modules, messages that do not parse, and messages whose arguments,
plural branches or select branches differ from the English ones. Run `fix`
after every edit: CI fails on a file that is not in canonical form.

## Message syntax

Messages use the subset of
[ICU MessageFormat](https://unicode-org.github.io/icu/userguide/format_parse/messages/)
that the server and the web interface
([FormatJS](https://formatjs.github.io/)) render identically:

| Form | Example |
|---|---|
| Argument | `Renew {domain} now` |
| Plural | `{count, plural, one {# certificate} other {# certificates}}` |
| Exact count | `{count, plural, =0 {no certificates} other {# certificates}}` |
| Select | `{kind, select, dns {DNS record} http {file} other {check}}` |

- Keep every argument of the English message, with the same name and of
  the same kind. Arguments may be moved, and a language without plural
  forms need not use them. The check compares these.
- In a plural branch, `#` stands for the number, written as the language
  writes numbers. Provide the plural categories the language has (`zero`,
  `one`, `two`, `few`, `many`, `other`; see
  [CLDR](https://www.unicode.org/cldr/charts/latest/supplemental/language_plural_rules.html));
  `other` is always required.
- An apostrophe quotes a following `{`, `}`, `<`, `>` or `#` up to the next
  apostrophe, so `'{{.domains}}'` stands for the text `{{.domains}}`; two
  consecutive apostrophes stand for one. Any other apostrophe is plain
  text: `don't` needs no escaping.
- Number, date and time arguments are formatted before they reach the
  message; there is no `{n, number}` or `{d, date}`.

## Style

- Write plain, short sentences. The interface is dense, especially on
  phones: the pseudo-locale test lengthens every text to 140% of the
  English and fails any page that overflows, so prefer the shorter of two
  equally accurate wordings.
- Keep names unchanged: product and service names (Let's Encrypt, Alibaba
  Cloud ESA, DNSPod), protocol and field names (ACME, CNAME,
  `_acme-challenge`), commands, paths and environment variables.
- Use the same term for a concept throughout. The English terms are:
  certificate source, validation method, deploy target, notification
  channel, issuance, renewal, deployment.
- Error messages state what happened and what to do next, without blaming
  the reader.

## Checking a translation

```sh
pnpm -C web build && go build -o build/jec ./cmd/jec
JEC_E2E_BIN=../build/jec pnpm -C web e2e  # the pseudo-locale test, through every page
```

Run JEC and switch the language on the Settings page, or let it follow the
browser's. Check a phone-sized window as well: the bottom tab bar, the
certificate list and the forms are where overlong text shows first.

## Plugins

A plugin provides the texts of its types in each language it supports
(see [Plugins](plugins.md)); for any other language, it falls back to
English, as JEC itself does.
