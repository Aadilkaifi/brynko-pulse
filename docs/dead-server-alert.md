# Dead-server alert

pulse alerts you about problems on a server, but a server that is down, cut off from the network,
or whose pulse stopped cannot send its own alert. The dead-server alert covers that: pulse checks
in with the brynko relay every minute, and when a server stops checking in for 5 minutes, the relay
emails you. It emails again when the server checks in again.

> **[Silent] web-1 (203.0.113.10) has not checked in for 5 minutes**
> pulse on web-1 last checked in at 10:02. The server may be down, cut off from the network, or
> pulse may have stopped.

It is off unless you turn it on. It needs a brynko account with credits; the alert email costs
nothing, and a WhatsApp alert through a brynko relay channel is charged like any relay message. While the account has no credits, check-ins are still recorded but the relay sends no
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
  media: [email]            # optional: email, whatsapp, or both (the default)
  notify: [ops-relay]       # optional: only these brynko relay channels; empty means every one
```

| Setting | Notes |
|---|---|
| `key` | This server's relay key, as a `${NAME}` reference to `secrets.env` |
| `media` | How the relay tells them: `email`, `whatsapp`, or both (leave it unset). For the dead-server alert by email only, while the channel's WhatsApp numbers keep getting the other alerts. With no address left for the media chosen, the relay emails the brynko account that turned the alert on |
| `notify` | Rarely needed. The [brynko relay](channels.md#brynko) channels to alert (their email addresses and WhatsApp numbers); empty means every brynko relay channel on the server. The relay sends this alert itself, because the server is silent, so it goes through its own channels only: any other channel named is skipped. With no address at all, the relay emails the brynko account that turned the alert on |
| `url` | Where to check in. Leave it unset |

`pulse validate` warns when a named channel is not a brynko relay channel, and when no relay channel has an address.

Before pulse 0.2.1 the alert also used email channels and, when `notify` was empty, the server's default channels.

## What the relay receives

Every minute: the server's name and address, pulse's version, how long pulse and the server have
been running, the email addresses and WhatsApp numbers to alert, and how long until the next
check-in. Nothing about your monitors, their values or your alerts. pulse sends nothing at all
unless `watchdog` is set.

While a server is silent the relay cannot know why: from outside, a server that is off, one that
lost its network and one whose pulse stopped look the same. When it checks in again, the two running
times let the "back" email say which it was:

- **The server started again**: it was switched off or restarted, at the time given.
- **pulse started again, the server stayed up**: pulse was stopped or restarted.
- **pulse kept running**: the server stayed up and could not reach the relay, most likely because
  its internet connection dropped.

Both times are durations, not clock times, so a server with a wrong clock gets the right answer.
On a Windows desktop with Fast Startup, a shutdown resumes rather than restarts Windows, so it reads
as "pulse started again"; Windows Server and a restart are not affected.

From pulse 0.2.2. With an older pulse the email only says how long the server was silent.

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
