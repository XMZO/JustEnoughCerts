# Plugins

[中文](plugins.zh-CN.md)

A plugin is a program, in any language, that gives JEC more deploy targets,
notification channels and ways to prove control of domains, without
rebuilding JEC. It describes the types it offers, with their forms and
texts in the languages it likes; JEC shows them as it shows its own, and
runs the plugin for each thing to do.

## Installing a plugin

- Put the program in the `plugins` directory of JEC's data: `/data/plugins`
  in the container (`./data/plugins` next to `docker-compose.yml`),
  `<data directory>/plugins` otherwise. Then restart JEC, which reads its
  plugins when it starts.
- The file's name without its extension is the plugin's ID: lowercase
  letters, digits, `-` and `_`, starting with a letter, at most 32 of them,
  such as `acme-cdn` or `my_dns.py`.
- The file must be executable. Neither it nor the directory may be writable
  by anyone but its owner, who must be root or the user JEC runs as
  (65532 in the container). JEC does not run other files.
- JEC's container image holds nothing but JEC, so a plugin there has to be
  a static binary, as Go and Rust make them. With JEC's own binary on a
  machine, scripts with a `#!` line work too.
- The Modules page lists the plugins JEC found, with the types each added,
  or why it is not in use. Switching the Plugins module off hides their
  types. JEC still asks each plugin to describe itself when it starts: to
  keep a plugin from running at all, remove it.

A plugin runs with the rights of JEC and gets the secrets of its settings:
install only plugins you trust.

## How JEC runs a plugin

Each action runs the plugin once, with no arguments.

- JEC writes one JSON request on its standard input, and closes it.
- The plugin writes one JSON answer on its standard output, 1 MiB at most,
  and exits. What it writes on its standard error goes to the log when the
  action fails.
- It runs in a directory of its own, `<plugins>/<ID>.data`, where it may
  keep files. Its environment is JEC's, without JEC's own `JEC_` settings,
  plus `JEC_PLUGIN` (its ID), `JEC_PLUGIN_DATA` (that directory) and
  `JEC_PLUGIN_PROTOCOL` (`1`).
- It has 10 seconds to describe itself, a minute for a test, and 5 minutes
  for anything else, or less when the caller has less, as 30 seconds to
  send a note. JEC then stops it, along with what it started.

Every request holds `"protocol": 1` and an `action`; every answer must hold
`"protocol": 1` and `ok`.

## describe

When JEC starts, it asks each plugin `{"protocol": 1, "action": "describe"}`.
The answer names the plugin and lists its types:

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

- A text is a string, for every language, or an object of strings by
  language tag. JEC shows the one of the user's language, else one of the
  same language, the string for every language, the English one, or the
  first. Texts show as they are, without markup.
- `kind` is `target` (deploys certificates), `notifier` (sends notes) or
  `solver` (proves control of domains). A solver's `challenge` is `dns`,
  the default, for TXT records, or `http` for files on the website. A DNS
  solver may say how long a record takes to be seen and how often to look,
  in seconds: `"wait": {"timeout": 300, "interval": 10}`, 120 and 5 when
  left out. A DNS solver with `"records": true` adds records of any type
  too, the TXT and CNAME records some certificate vendors validate domains
  with, so that sources such as Namecheap's or Alibaba Cloud's can use it.
- A field has a `key` (letters, digits and `_`, starting with a letter) and
  a `kind`: `text`, `multiline`, `number`, `url`, `email`, `select` or
  `switch` (`"true"` or `"false"`). It may have a `label`, a `help` under
  it, a `default`, `options` for a select, and be `required`, `secret`
  (sealed, and never shown again once saved), `advanced` (apart in the
  form) or shown only `when` an earlier plain field holds a value.
- Notifiers get JEC's own fields as well: the language of the notes and
  which events to send. JEC writes the notes in that language.
- A plugin offers 32 types at most, of 64 fields at most. Their IDs follow
  the rules of plugin IDs; JEC calls a type `plugins.<plugin>.<type>`.
  Keep the IDs of types and fields once they are in use: the settings
  people saved refer to them.

## Actions

Every other request holds `type`, the ID of the type, and the settings of
the configured instance: `config`, the plain ones, and `secrets`, by key.

| Action | The request also holds | The answer may hold |
|---|---|---|
| `test` | | `message`, what the plugin found, shown to the user |
| `deploy` | `certificate`, `previous` | `ref`, `removed` |
| `notify` | `note` | |
| `dns_present`, `dns_cleanup` | `record` | |
| `dns_add_record`, `dns_remove_record` | `dns_record`, and `ref` to remove | `ref`, to add |
| `file_put`, `file_remove` | `file` | |

- `test` comes for types marked `testable`, from the button on their
  card. It checks the settings, and changes nothing.
- `deploy` installs a new version. `certificate` holds its `id`,
  `domains` (the primary first), `serial` (hex), `fingerprint` (the
  SHA-256 of the leaf, hex), `not_before` and `not_after` (RFC 3339), and
  the PEM `cert`, `chain`, `fullchain` and `privkey`, named as certbot's
  files. `previous` lists what earlier deployments of the certificate to
  this instance answered as `ref`, the newest first. The answer's `ref`
  names what the target holds now, such as a certificate's ID at a CDN, to
  come back in `previous` next time; `removed` lists those of `previous`
  the plugin removed.
- `notify` sends `note`: its `event` (such as `certificate.issue_failed`,
  or `notify.test` for a test), `title`, `subject` (the title marked as
  JEC's, for places where many senders meet), `text`, `link` (the page in
  JEC, when JEC knows its URL), `time` and `data`, the event's own.
- `dns_present` adds, and `dns_cleanup` removes, the TXT record of a DNS-01
  validation: `record` holds its `domain`, its name `fqdn` (such as
  `_acme-challenge.example.com.`) and its `value`.
- `dns_add_record` adds, and `dns_remove_record` removes, a record of any
  type, for a solver with `records`: `dns_record` holds its full `name`
  (such as `_dnsauth.example.com`, without a final dot), its `type`, `TXT`
  or `CNAME`, and its `value`. The answer to `dns_add_record` may hold a
  `ref`, such as the record's ID at the provider, which `dns_remove_record`
  gets back as `ref`.
- `file_put` puts, and `file_remove` removes, a file the website must serve
  at `http://<domain><path>`: `file` holds the `domain`, the `path` (under
  `/.well-known/`) and the `content`.

## Answers

- Success: `{"protocol": 1, "ok": true}`, with what the action may add.
- Failure: `{"protocol": 1, "ok": false, "error": "What went wrong", "retry": true, "retry_after": 60}`.
  JEC shows `error` as it is. With `retry`, JEC tries again later: after
  `retry_after` seconds when given, after a growing delay otherwise.
  Without it, JEC gives up and lets the user know.
- An answer may add lines to the log of the deployment it is part of,
  100 at most: `"log": [{"level": "ok", "text": "Uploaded"}]`, where
  `level` is `info`, `ok`, `warn` or `error`.
- A plugin that gives no answer, or runs out of time, has failed in a way
  worth trying again; an answer JEC cannot read has not.

## An example

A plugin in Python, for JEC's own binary on a machine: a deploy target
that writes the certificate and key to a directory.

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
make it executable with `chmod 755`, and restart JEC: the type shows among
the deploy targets, under Plugins.
