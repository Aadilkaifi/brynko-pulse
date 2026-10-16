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
channel's last delivery or error. All fields are in the [configuration reference](config-reference.md).

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
`summary`, `test`), `severity`, `detail`, `condition`, `rule` (the condition in words), `since`,
`at` and `duration_ns`. Fields with no value are left out. With a `secret`, each request carries `X-Pulse-Timestamp` and
`X-Pulse-Signature: sha256=<hex>`, the HMAC-SHA256 of `<timestamp>.<body>` with the secret. Check
it, and reject old timestamps, to know the request came from your pulse.

## `brynko`

Not available yet. The brynko relay will send WhatsApp and email for you, paid from brynko
account credits, for people who do not want their own mail server or Meta account.
