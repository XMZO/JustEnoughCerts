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
| The CA revoked a certificate (see [Revocation checks](renewal.md#revocation-checks)) | On |
| A watched site's certificate ends soon or ended (see [Expiry watch](sites.md)) | On |
| A domain registration ends soon or ended (likewise) | On |
| New certificate (issued or uploaded) | Off |
| Deployment succeeded | Off |
| Weekly digest, on Monday from 9:00 in the panel's time zone: certificates needing attention and renewals of the coming week | Off |

The weekly digest also tells that JEC is running: a week without one is
worth a look. Each channel has its own events and message language. Message subjects
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

## Commands and menu of a Telegram bot

Besides sending notifications, a Telegram bot can answer commands in its
chat:

1. Edit the bot among the notification channels and turn on "Commands
   and menu". To act from Telegram, also turn on "Allow actions from
   Telegram".
2. Send `/menu` in the chat. The bot replies with a menu; its buttons
   update the same message in place.

| Command | Shows |
|---|---|
| `/menu` | The main menu: how many certificates are in which state, the next renewal, and the way to everything else |
| `/certs` | Every certificate with the days it has left; each opens its own page |
| `/todo` | The certificates that need attention, and why |
| `/recent` | The issuances, renewals and deployments of the last 7 days |
| `/online` | What the online checks found wrong |
| `/sites` | The [watched sites and domain registrations](sites.md), those to look at first |
| `/help` | How to use it |

A certificate's page shows its state, the days it has left, its renewal,
its source and issuer, its deployments, its online checks and its last
issuance. When actions are allowed, it also offers Renew now, Deploy again
and Check now; renewing and deploying ask for confirmation first.

- The bot answers only the chat set as its Chat ID. Another chat that
  sends `/start` learns only its own ID, to set up the bot with.
- The bot reads and acts as the panel's account, but cannot change the
  account; the audit log shows its actions "through <the bot's name>".
- While the bot receives commands, its notes carry a button: a note about
  a certificate opens its page, a reminder about sites or domains the
  expiry watch, the weekly digest the menu. What the button shows comes as
  a new message, and the note stays as it was.
- One bot token answers commands in one place only. A bot with a webhook
  set cannot answer commands.
- JEC answers only the commands sent once it listens; those sent while it
  was down are not carried out later.

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

## Calendar feed

At the bottom of the Notifications page, the calendar feed shows when
certificates end, with a reminder a week before, and when their next
renewal starts, in calendar apps such as those of Apple, Google and
Microsoft. With the expiry watch module on, it also shows when the
certificates of watched sites and domain registrations end.

1. Turn the calendar feed on: JEC shows its URL.
2. Choose Add to calendar on the device, or copy the URL and add it as a
   subscription by URL in the calendar app. The events are in the language
   of the interface when the URL was copied; `lang=en-US` or `lang=zh-CN`
   at its end chooses another.

The URL holds a secret key, as calendar apps cannot sign in: anyone with
it sees the domains and dates. New URL makes another key, after which the
old URL stops working; turning the feed off stops it too. The module
"Calendar feed" provides it and can be switched off on the Modules page.

## Plugins

Plugins can provide new notification channels; see
[Plugins](../development/plugins.md).
