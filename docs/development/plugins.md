# Plugins

[中文](plugins.zh-CN.md)

A plugin is a standalone program, written in any language, that adds
certificate sources, deploy targets, notification channels and domain
validation methods to JEC without rebuilding it. A plugin declares the
types it provides, with their form fields and texts in several languages;
JEC presents those types as it presents its own, and runs the plugin for
each operation.

## Installation

- Place the program in the `plugins` directory of JEC's data directory:
  `/data/plugins` in the container (`./data/plugins` next to
  `docker-compose.yml`), `<data directory>/plugins` otherwise. JEC reads
  its plugins when it starts, so restart it afterwards.
- The plugin's ID is its file name without the extension: lowercase
  letters, digits, `-` and `_`, starting with a letter, at most 32
  characters, such as `acme-cdn` or `my_dns.py`.
- The file must be executable. The file and its directory must be
  writable by their owner only, who must be root or the user JEC runs as
  (65532 in the container); JEC does not run other files.
- JEC's container image contains nothing but JEC, so a plugin in the
  container must be a statically linked program, such as one built with Go
  or Rust. When JEC's binary runs directly on a machine, scripts with a
  `#!` line work as well.
- The Modules page lists the plugins JEC has found, the types each one
  adds, and the reason a plugin is not in use. Disabling the Plugins
  module hides their types, but JEC still asks each plugin to describe
  itself at startup; to keep a plugin from running at all, remove it.

A plugin runs with JEC's privileges and receives the secrets of its
settings; install only plugins you trust.

## Execution

Each operation runs the plugin once, without command-line arguments.

- JEC writes one JSON request to the plugin's standard input, then closes
  it.
- The plugin writes one JSON answer, at most 1 MiB, to its standard output
  and exits. Whatever it writes to standard error is logged when the
  operation fails.
- The plugin's working directory is `<plugins>/<ID>.data`, where it may
  keep files. Its environment is JEC's, without JEC's own `JEC_` settings,
  plus `JEC_PLUGIN` (its ID), `JEC_PLUGIN_DATA` (that directory) and
  `JEC_PLUGIN_PROTOCOL` (`1`).
- Time limits: 10 seconds to describe itself, one minute for a test and
  five minutes for any other operation, or less when the caller's limit is
  shorter, such as 30 seconds to send a notification. When time runs out,
  JEC stops the plugin and the processes it started.

Every request contains `"protocol": 1` and an `action`; every answer must
contain `"protocol": 1` and `ok`.

## describe

At startup, JEC sends each plugin `{"protocol": 1, "action": "describe"}`.
The answer names the plugin and lists the types it provides:

```json
{
  "protocol": 1,
  "ok": true,
  "name": {"en-US": "Acme CDN", "zh-CN": "Acme CDN 加速"},
  "description": "Deploys certificates to Acme CDN.",
  "types": [
    {
      "id": "cdn",
      "kind": "target",
      "testable": true,
      "name": {"en-US": "Acme CDN", "zh-CN": "Acme CDN 加速"},
      "description": "Uploads each new certificate, then removes the one it replaces.",
      "fields": [
        {"key": "api_token", "kind": "text", "label": "API token", "secret": true, "required": true},
        {"key": "region", "kind": "select", "label": "Region", "default": "eu",
         "options": [{"value": "eu", "label": "Europe"}, {"value": "us", "label": "America"}]},
        {"key": "bucket", "kind": "text", "label": "Bucket", "advanced": true,
         "when": {"key": "region", "value": "eu"}}
      ]
    }
  ]
}
```

- A text is either a string, used for every language, or an object of
  strings keyed by language tag. JEC uses, in this order: the text in the
  user's language, a text in another region of the same language, the
  string for every language, the English text, or the first one. Texts are
  shown as they are, without markup.
- `kind` is `source` (issues or retrieves certificates), `target` (deploys
  certificates), `notifier` (sends notifications) or `solver` (validates
  domains).
- A solver's `challenge` is `dns`, the default, for TXT records, or `http`
  for files on the website. A DNS solver may state, in seconds, how long a
  record takes to become visible and how often to check:
  `"wait": {"timeout": 300, "interval": 10}`, which default to 120 and 5.
  A DNS solver with `"records": true` can also add records of any type,
  such as the TXT and CNAME records some certificate vendors validate
  domains with, for sources such as Namecheap's or Alibaba Cloud's.
- A source issues certificates from a CSR, for a private key that JEC
  generates. When its CA validates domains by files on the website or by
  DNS records, it declares `http`, `record` or both in `challenges`; a
  certificate from it must then use a validation method that meets them,
  and JEC puts in place what the plugin asks for (see `issue`). A source
  with `"own_key": true` instead provides ready certificates with their own
  keys, such as from a certificate store or another tool, and validates
  nothing through JEC.
- A field has a `key` (letters, digits and `_`, starting with a letter)
  and a `kind`: `text`, `multiline`, `number`, `url`, `email`, `select` or
  `switch` (`"true"` or `"false"`). It may also have a `label`, a `help`
  text shown below it, a `default`, `options` for a select, and be
  `required`, `secret` (stored encrypted and never shown again once
  saved), `advanced` (grouped under the advanced settings of the form), or
  shown only `when` an earlier plain field holds a given value.
- Notifiers also receive JEC's common fields: the message language and the
  events to send. JEC writes the notifications in that language before
  handing them to the plugin.
- A plugin provides at most 32 types, of at most 64 fields each. Type IDs
  follow the rules of plugin IDs; JEC names a type
  `plugins.<plugin>.<type>`. Do not change the IDs of types and fields once
  they are in use: the settings users saved refer to them.

## Operations

Every request other than `describe` contains `type`, the ID of the type,
and the settings of the configured instance: `config`, the plain
settings, and `secrets`, both keyed by field.

| Operation | The request also contains | The answer may contain |
|---|---|---|
| `test` | | `message`: the result of the plugin's check, shown to the user |
| `deploy` | `certificate`, `previous` | `ref`, `removed` |
| `notify` | `note` | |
| `dns_present`, `dns_cleanup` | `record` | |
| `dns_add_record`, `dns_remove_record` | `dns_record`; for removal, also `ref` | for addition, `ref` |
| `file_put`, `file_remove` | `file` | |
| `issue` | `issue` | `chain`, or `wait` |
| `collect` | `collect` | `chain` and `key`, or `wait` |

- `test` is sent only to types marked `testable`, from the Test button on
  their card. It checks the settings and changes nothing.
- `deploy` installs a new version. `certificate` contains its `id`,
  `domains` (the primary domain first), `serial` (hex), `fingerprint` (the
  SHA-256 of the leaf, hex), `not_before` and `not_after` (RFC 3339), and
  the PEM `cert`, `chain`, `fullchain` and `privkey`, named after certbot's
  files. `previous` lists the `ref` values that earlier deployments of the
  certificate to this instance returned, newest first. The answer's `ref`
  identifies what the target now holds, such as a certificate's ID at a
  CDN, and comes back in `previous` next time; `removed` lists the entries
  of `previous` the plugin removed.
- `notify` sends `note`, which contains its `event` (such as
  `certificate.issue_failed`, or `notify.test` for a test), `title`,
  `subject` (the title marked as JEC's, for places shared by many senders),
  `text`, `link` (the page in JEC, present only when JEC knows its own
  URL), `time` and `data`, the event's own data.
- `dns_present` and `dns_cleanup` add and remove the TXT record of a DNS-01
  validation. `record` contains its `domain`, its name `fqdn` (such as
  `_acme-challenge.example.com.`) and its `value`.
- `dns_add_record` and `dns_remove_record` add and remove a record of any
  type, and are sent only to solvers with `records`. `dns_record` contains
  its full `name` (such as `_dnsauth.example.com`, without a final dot),
  its `type`, `TXT` or `CNAME`, and its `value`. The answer to
  `dns_add_record` may contain a `ref`, such as the record's ID at the
  provider, which `dns_remove_record` receives back as `ref`.
- `file_put` and `file_remove` put and remove a file that the website must
  serve at `http://<domain><path>`. `file` contains the `domain`, the
  `path`, under `/.well-known/`, and the `content`.
- `issue` asks a source for a certificate. `issue` contains its `domains`
  (the primary domain first, wildcards as `*.example.com`) and the PEM
  `csr`, signed by the private key JEC generated for the issuance, with
  the primary domain as its subject and every domain among its names.
  Every attempt of one issuance sends the same key, so the plugin can
  continue an order it placed earlier. After a wait, `issue` also contains
  the `state` that wait returned, the `files` JEC put in place (`domain`
  and `path`) and the `records` it added (`name`, `type`, `value` and
  `visible`, which tells whether the authoritative DNS servers of the zone
  already answer it). The answer contains the PEM `chain`, the leaf first,
  for the CSR's key and covering every domain, or a `wait` (see below).
- `collect` asks a source with `own_key` for a certificate newer than the
  one in use. `collect` contains its `domains`; when a certificate is in
  use, also `current`, the SHA-256 of its leaf (hex), and `current_end`,
  its expiry (RFC 3339); and after a wait, the `state`. The answer
  contains the PEM `chain` and the certificate's private `key`, or a `wait`
  when there is nothing newer.

## Waiting

When the certificate is not ready yet, a source answers
`"wait": {"after": 300, "state": "..."}`. JEC asks again after `after`
seconds, from one minute to one day, and returns `state` unchanged; its
content is up to the plugin. Meanwhile, the issuance is shown as waiting
rather than failed; the plugin decides when to give up, by answering with
a failure.

The wait of a source that issues certificates may declare the `files`
(`domain`, `path` under `/.well-known/`, and `content`) and the `records`
(`domain`, `name`, `type`, `TXT` or `CNAME`, and `value`) its validation
needs. Before waiting, JEC puts them in place through the certificate's
validation method: it keeps those that later waits still list, removes
those they no longer list, and removes the rest once the plugin answers
with the certificate. The `domain` of each must be one of the
certificate's domains; a record's `name` is its full name, or a name
relative to the domain, as consoles show it (`_dnsauth` for
`_dnsauth.example.com`). If JEC cannot put one in place, the attempt
fails, and the next attempt receives the `state` of this wait.

The following example shows a source for a CA that validates by DNS. The
request:

```json
{"protocol": 1, "action": "issue", "type": "dv", "config": {}, "secrets": {"api_key": "..."},
 "issue": {"domains": ["example.com", "www.example.com"], "csr": "-----BEGIN CERTIFICATE REQUEST-----..."}}
```

The plugin places the order and answers with the records the CA checks:

```json
{"protocol": 1, "ok": true, "wait": {"after": 300, "state": "order-12345",
 "records": [{"domain": "example.com", "name": "_dnsauth", "type": "TXT", "value": "abc123"},
             {"domain": "www.example.com", "name": "_dnsauth", "type": "TXT", "value": "def456"}]}}
```

Five minutes later, JEC asks again:

```json
{"protocol": 1, "action": "issue", "type": "dv", "config": {}, "secrets": {"api_key": "..."},
 "issue": {"domains": ["example.com", "www.example.com"], "csr": "-----BEGIN CERTIFICATE REQUEST-----...",
  "state": "order-12345",
  "records": [{"name": "_dnsauth.example.com", "type": "TXT", "value": "abc123", "visible": true},
              {"name": "_dnsauth.www.example.com", "type": "TXT", "value": "def456", "visible": true}]}}
```

The plugin asks the CA to check, if it has not yet, and answers with the
same wait until the CA issues the certificate; it then answers
`{"protocol": 1, "ok": true, "chain": "-----BEGIN CERTIFICATE-----..."}`,
and JEC removes the records.

## Answers

- Success: `{"protocol": 1, "ok": true}`, with whatever the operation
  returns.
- Failure: `{"protocol": 1, "ok": false, "error": "What went wrong", "retry": true, "retry_after": 60}`.
  JEC shows `error` as it is. With `retry`, JEC tries again later: after
  `retry_after` seconds when given, otherwise after a growing delay.
  Without it, JEC gives up and informs the user.
- An answer may append up to 100 lines to the log of the deployment or
  issuance it belongs to: `"log": [{"level": "ok", "text": "Uploaded"}]`,
  where `level` is `info`, `ok`, `warn` or `error`.
- A plugin that does not answer, or runs out of time, has failed in a way
  worth retrying; an answer JEC cannot parse is not retried.

## Example

The following Python plugin implements a deploy target that writes the
certificate and its key to a directory, for use when JEC's binary runs
directly on a machine.

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
        answer(ok=True, message=f"{d} is there")
    else:
        answer(ok=False, error=f"There is no directory {d}")
elif req["action"] == "deploy":
    d, cert = req["config"]["dir"], req["certificate"]
    for name in ("fullchain", "privkey"):
        path = os.path.join(d, name + ".pem")
        with open(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), "w") as f:
            f.write(cert[name])
    answer(ok=True, log=[{"level": "ok", "text": f"Wrote {d}"}])
else:
    answer(ok=False, error="Unknown action " + req["action"])
```

Save it as `copy.py` in the `plugins` directory of JEC's data directory,
make it executable with `chmod 755`, and restart JEC: the type appears
among the deploy targets, under Plugins.
