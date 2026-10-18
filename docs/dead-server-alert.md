# Dead-server alert

pulse alerts you about problems on a server, but a server that is down, cut off from the network,
or whose pulse stopped cannot send its own alert. The dead-server alert covers that: pulse checks
in with the brynko relay every minute, and when a server stops checking in for 5 minutes, the relay
emails you. It emails again when the server checks in again.

> **[Silent] web-1 (203.0.113.10) has not checked in for 5 minutes**
> pulse on web-1 last checked in at 10:02. The server may be down, cut off from the network, or
> pulse may have stopped.

It is off unless you turn it on. It needs a brynko account with credits; the alert itself costs
nothing. While the account has no credits, check-ins are still recorded but the relay sends no
alerts, and `pulse status` says so.

## Turn it on

The easiest way is the brynko devOps Agent app: on the server's Monitoring page, turn on **Alert me
if this server goes silent**. The app gets a key for the server from your account, stores it in
`secrets.env` and adds the setting below.

By hand, with a key from your brynko account:

```sh
sudo pulse secret set PULSE_BRYNKO_KEY
```

```yaml
# /etc/brynko-pulse/pulse.yaml
watchdog:
  key: ${PULSE_BRYNKO_KEY}
  notify: [ops-email]       # optional: the email channels to alert; empty means defaults.notify
```

| Setting | Notes |
|---|---|
| `key` | This server's relay key, as a `${NAME}` reference to `secrets.env` |
| `notify` | Email channels whose `to` addresses the relay alerts. Other channels are skipped: Telegram, Slack, WhatsApp and webhook addresses hold secrets that never leave the server. Empty: the server's default channels |
| `url` | Where to check in. Leave it unset |

`pulse validate` warns when a named channel is not email, and when nobody would be emailed.

## What the relay receives

Every minute: the server's name and address, pulse's version, the email addresses to alert, and how
long until the next check-in. Nothing about your monitors, their values or your alerts. pulse sends
nothing at all unless `watchdog` is set.

## Planned stops

- **A reboot** usually takes less than 5 minutes, and pulse checks in again within seconds of
  starting, so it does not raise an alert.
- **`pulse uninstall`** tells the relay to stop watching the server first, so removing pulse on
  purpose does not raise one. If the relay cannot be reached then, turn the alert off in the app.
- **Stopping the pulse service** for longer than 5 minutes does raise one: from outside, a stopped
  pulse looks the same as a dead server.

## Seeing how it goes

- `pulse status` ends with a line such as `Dead-server alert: on, checked in 20s ago; alerts
  ops@example.com`, or the last error.
- `pulse status --json` has `watchdog` with `last_ok`, `last_error`, `last_error_at`, `email` and
  `skipped` (channels it cannot use).
