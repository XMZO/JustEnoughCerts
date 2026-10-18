# Translating JEC

[中文](translating.zh-CN.md)

Every text JEC shows, in the web UI, in error messages, in issuance logs
and in notifications, lives in one JSON file per language under
[`i18n/locales/`](../i18n/locales). The server embeds those files and the
web UI loads the same ones, so a language added there works everywhere at
once. American English (`en-US.json`) is the base: it defines the keys,
and stands in for any message another language leaves out.

## Adding a language

1. Copy `i18n/locales/en-US.json` to a file named by the language's
   [BCP 47 tag](https://www.w3.org/International/articles/language-tags/),
   such as `de-DE.json` or `pt-BR.json`.
2. Fill in `_meta`: `name` is the language's name in itself (`Deutsch`),
   `dir` is `ltr`, or `rtl` for Arabic, Hebrew and the like. Leave
   `completeness` alone: the tools keep it.
3. Translate the values. Leave the keys, and the names in braces, as they
   are.
4. Run `go run ./tools/i18n fix` and `go generate ./i18n` (below), which
   adds the language to the web UI's list. It then shows up in the
   language menu of the Settings page, and notification channels can be set
   to it.

A language need not be complete: what it leaves out shows in English, and
its `completeness` says how much is done. Only `en-US` and `zh-CN` must
translate every key, so a partial translation is welcome as a start.

## The tools

```sh
go run ./tools/i18n fix    # sort the files, write completeness
go run ./tools/i18n check  # the checks CI runs
go generate ./i18n         # after adding or removing keys
```

`check` reports keys missing from the base language or from `zh-CN`, keys
the base language does not have, empty texts, the `TODO:` placeholders of
new modules, messages that do not parse, and messages whose arguments,
plural or select branches differ from the English ones. Run `fix` after
each edit: CI fails on a file that is not in its canonical form.

## Message syntax

Messages use the part of
[ICU MessageFormat](https://unicode-org.github.io/icu/userguide/format_parse/messages/)
that the server and the web UI ([FormatJS](https://formatjs.github.io/))
render alike:

| Form | Example |
|---|---|
| An argument | `Renew {domain} now` |
| A plural | `{count, plural, one {# certificate} other {# certificates}}` |
| An exact count | `{count, plural, =0 {no certificates} other {# certificates}}` |
| A select | `{kind, select, dns {DNS record} http {file} other {check}}` |

- Keep every argument of the English message, by the same name and of the
  same kind; you may move them, and need not use a plural where your
  language has none. The check compares them.
- In a plural branch, `#` is the number, written as the language writes
  numbers. Write the plural categories your language has (`zero`, `one`,
  `two`, `few`, `many`, `other`, after
  [CLDR](https://www.unicode.org/cldr/charts/latest/supplemental/language_plural_rules.html));
  `other` is always required.
- An apostrophe quotes a following `{`, `}`, `<`, `>` or `#` up to the next
  apostrophe, so `'{{.domains}}'` stands for the text `{{.domains}}`; two
  apostrophes stand for one. Any other apostrophe is plain text: `don't`
  needs nothing.
- Number, date and time arguments are formatted before they reach the
  message: there is no `{n, number}` or `{d, date}`.

## Style

- Plain, short sentences. The UI is dense, notably on phones: the
  pseudo-locale test makes every text 40% longer than English and fails a
  page that overflows, so prefer the shorter of two good wordings.
- Keep names as they are: product and service names (Let's Encrypt,
  Alibaba Cloud ESA, DNSPod), protocol and field names (ACME, CNAME,
  `_acme-challenge`), commands, paths and environment variables.
- Use the same word for a concept everywhere. In English: certificate
  source, validation method, deploy target, notification channel, issuance,
  renewal, deployment.
- Error messages say what happened and what to do next, without blaming
  the reader.

## Checking the result

```sh
pnpm -C web build && go build -o build/jec ./cmd/jec
JEC_E2E_BIN=../build/jec pnpm -C web e2e  # the pseudo-locale walk through every page
```

Run JEC and switch the language on the Settings page, or let it follow the
browser's. Look at a phone-sized window too: the bottom tab bar, the
certificate list and the forms are where long words show first.

## Plugins

A plugin brings the texts of its types along, in each language it knows
([docs/plugins.md](plugins.md)); a language it does not know falls back to
English, like JEC's own.
