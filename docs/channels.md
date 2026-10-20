# Channels

A channel is somewhere alerts go. Channels live in `channels.yaml`, each under a name you choose.
A monitor sends to the channels in its `notify` list, or to `defaults.notify` in `pulse.yaml`.

Secrets (passwords, tokens, webhook addresses) are never written in `channels.yaml`. Write
`${NAME}` instead and put `NAME=value` in `secrets.env` in the same folder:

```yaml
# channels.yaml
channels:
  ops-email:
    type: smtp
    host: smtp.example.com
    from: pulse@example.com
    to: [ops@example.com]
    username: pulse@example.com
    password: ${PULSE_SMTP_PASSWORD}
```

```sh
# secrets.env (readable only by administrators and the pulse service)
PULSE_SMTP_PASSWORD=my-app-password
```

After adding a channel:

```sh
sudo pulse validate                  # checks everything and says what to fix
sudo pulse test channel ops-email    # sends a real test alert
sudo systemctl reload brynko-pulse   # Windows: sc.exe control brynko-pulse paramchange
```

Every channel can set `min_severity` (`info`, `warning`, `critical`) to receive only the more
urgent alerts. Failed deliveries are retried after 2 and 10 seconds, and `pulse status` shows each
channel's last delivery or error; pulse keeps it through a reload or a restart while that channel's
settings and `secrets.env` stay the same. An alert still not delivered after the retries is also written to
the history, as kind `undelivered` with the channel and its error. All fields are in the [configuration reference](config-reference.md).

## `smtp`

Email through any mail server, including Gmail, Outlook and Amazon SES.

| Setting | Notes |
|---|---|
| `host`, `port` | Port 587 with `tls: starttls` (the default) suits most providers; 465 needs `tls: tls` |
| `username`, `password` | Most providers need an app password, not your account password |
| `from`, `to` | `to` is a list |

## `whatsapp`

WhatsApp through your own Meta WhatsApp Cloud API account. You pay Meta's message prices; pulse
adds nothing.

1. In Meta for Developers, create an app with WhatsApp, and note the **phone number ID** and a
   **permanent access token**.
2. Create a message template, for example `pulse_alert`, category Utility, with a body that has two
   variables, such as: `{{1}}` and `Details: {{2}}`. Wait for Meta to approve it.
3. Add the channel:

```yaml
  my-whatsapp:
    type: whatsapp
    phone_number_id: "123456789012345"
    access_token: ${PULSE_WHATSAPP_TOKEN}
    template: pulse_alert
    language: en              # the template's language code
    to: ["+919876543210"]     # international format
```

pulse fills `{{1}}` with the alert's subject and `{{2}}` with its details, shortened to fit
WhatsApp's limits.

## `telegram`

1. Talk to `@BotFather` in Telegram, create a bot, and copy its token.
2. Send your bot a message (or add it to a group), then find the chat id, for example from
   `https://api.telegram.org/bot<token>/getUpdates`.

```yaml
  team-telegram:
    type: telegram
    bot_token: ${PULSE_TELEGRAM_TOKEN}
    chat_id: "-1001234567890"
```

## `slack`

Create an incoming webhook in Slack (Apps, Incoming Webhooks) and keep its URL in `secrets.env`:

```yaml
  team-slack:
    type: slack
    webhook_url: ${PULSE_SLACK_WEBHOOK}
```

## `webhook`

Posts each alert as JSON to any URL, for your own tools.

```yaml
  my-tool:
    type: webhook
    url: https://hooks.example.com/pulse
    secret: ${PULSE_WEBHOOK_SECRET}   # optional
```

The body has `server`, `server_address` (when set), `monitor`, `monitor_name`, `monitor_type`,
`type_label` (the type in words), `kind` (`firing`, `repeat`, `resolved`, `flapping`, `settled`,
`healed`, `heal_failed`, `summary`, `test`), `severity`, `detail`, `condition`, `rule` (the
condition in words), `heal` (what [self-healing](self-healing.md) did), `since`, `at` and
`duration_ns`. Fields with no value are left out. With a `secret`, each request carries `X-Pulse-Timestamp` and
`X-Pulse-Signature: sha256=<hex>`, the HMAC-SHA256 of `<timestamp>.<body>` with the secret. Check
it, and reject old timestamps, to know the request came from your pulse.

## `brynko`

The brynko relay sends alerts by WhatsApp and email for you, so you need no mail server, no Meta
account and no message template of your own. It is a paid service from brynko, the makers of pulse,
used from brynko account credits. pulse itself stays free and works fully without it; every other
channel on this page is yours to run.

The easiest way is the brynko devOps Agent app: add a channel of type **brynko relay** on the server's
Monitoring page. The app gets this server's key from your account and stores it in `secrets.env`.

By hand:

1. Create a brynko account at [cloudagent.brynko.in](https://cloudagent.brynko.in) and add credits.
2. Get this server's key from your account and keep it in `secrets.env`, for example
   `PULSE_BRYNKO_KEY=...` (the [dead-server alert](dead-server-alert.md) uses the same key).
3. Add the channel:

```yaml
  brynko-relay:
    type: brynko
    api_key: ${PULSE_BRYNKO_KEY}
    whatsapp: ["+919876543210"]       # international format; optional
    email: [ops@example.com]          # optional; at least one of whatsapp and email
```

| Setting | Notes |
|---|---|
| `api_key` | The relay key, as a `${NAME}` reference; one key per server is the safest |
| `whatsapp` | Phone numbers in international format |
| `email` | Email addresses |
| `min_severity` | As for every channel, for example `critical` to pay only for the alerts that matter |
| `url` | Where the relay takes alerts. Leave it unset |

**What the relay receives:** the alert itself (the server's name and address, the monitor, what is
wrong, when it started), nothing else from the server. It is used only to deliver that message.

**Costs:** each message delivered uses credits from your brynko account; the prices are on the brynko
site. When the credits run out, the relay stops delivering and `pulse status` shows the channel's
last error ("the account has no credits left for messages"), so keep a second channel (email through
your own server, Telegram or Slack) for critical alerts.

A brynko relay channel can also receive the [dead-server alert](dead-server-alert.md): name it in
`watchdog.notify`, and its numbers get a WhatsApp message when the server goes silent.
