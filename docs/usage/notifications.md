# Notifications

[中文](notifications.zh-CN.md)

Notification channels are managed on the Notifications page. When a chosen
event happens to a certificate, JEC writes a message in each channel's
language and sends it. Send a test, on a channel's card, sends a test
message at once.

## Events

| Event | Default |
|---|---|
| Issuance failed | On |
| Deployment failed | On |
| Certificate expiring or expired | On |
| Old certificate still served (found by the online check) | On |
| New certificate (issued or uploaded) | Off |
| Deployment succeeded | Off |

Each channel has its own events and message language. Message subjects
start with "【JEC】", to stand out in group chats; when `JEC_BASE_URL` is
set, messages link to the pages they concern. An event is never sent twice
to the same channel, and messages not delivered within three days are
dropped.

## Channels

| Channel | Description |
|---|---|
| Email | Sent through SMTP |
| Telegram | Bot token and chat ID; a proxy and a self-hosted API address can be set |
| DingTalk, Feishu (Lark), WeCom | Group robots; the keyword can be `JEC`; DingTalk and Feishu support signing |
| ServerChan | The Turbo edition and ServerChan³, told apart by the SendKey |
| Bark | The official server by default, or a self-hosted one |
| ntfy, Gotify | Self-hosted servers are supported |
| Discord | The channel's webhook URL |
| Slack | Incoming webhooks; Mattermost and Rocket.Chat are compatible |
| Pushover | User key and application token |
| Webhook | JSON or a custom template; signing key, headers, method and Content-Type can be set |
| Run a script | Runs a script from the `hooks/` directory of the data directory with the built-in shell; see below |

Channels with self-hosted servers (Bark, ntfy, Gotify, Slack and Webhook)
have an option to allow private addresses; by default they connect to public
addresses only. Channels that reach services abroad (Telegram, Discord,
Slack, Pushover, ntfy, Gotify, Bark and Webhook) accept a proxy of their
own in the advanced settings.

## Running a script

The script channel integrates other systems:

- Scripts live in the `hooks/` directory of the data directory, created on
  first use and accessible to its owner only. The built-in shell,
  compatible with POSIX sh and Bash, runs them for at most 20 seconds.
- The message is passed in the environment variables `JEC_EVENT`,
  `JEC_TIME`, `JEC_TITLE`, `JEC_SUBJECT`, `JEC_TEXT` and `JEC_LINK`; for an
  event about a certificate, also `JEC_CERTIFICATE_ID`, `JEC_DOMAIN`,
  `JEC_DOMAINS`, `JEC_NOT_AFTER` and `JEC_FINGERPRINT`. Standard input
  carries the whole message as JSON, in the format of the webhook.
- Exit status 0 means success; 75 means a temporary failure, retried
  later; any other status, a syntax error or a timeout is not retried.

## Plugins

Plugins can provide new notification channels; see
[Plugins](../development/plugins.md).
