# brynko-pulse

Open-source monitoring and alerts for Linux and Windows servers. One small program watches the
server and tells you within seconds when something breaks, through the channels you choose: email,
WhatsApp, Telegram, Slack or any webhook.

> **Status: early development.** Nothing here is ready for production yet. Today `pulse` can check
> a configuration; monitoring and alerting come next.

## Why

- **Runs on the server itself.** No account, no cloud service, no telemetry.
- **Outbound only.** It never opens a network port.
- **Plain files.** Configuration is YAML, checked against a published JSON Schema, so mistakes are
  caught with a clear message before anything runs.
- **Quiet when it should be.** Alerts wait for a problem to last, recover with a margin, and fold
  storms into one message, so you trust the ones you get.

## Try it

```sh
go build -o pulse ./cmd/pulse
sudo ./pulse init        # creates the config folder with a commented starter setup
sudo ./pulse validate    # checks it and tells you what to do next
```

`pulse init` creates `/etc/brynko-pulse` on Linux or `%ProgramData%\brynko-pulse` on Windows
(run it as Administrator there), and never overwrites existing files:

```
pulse.yaml        server name and defaults
channels.yaml     where alerts go
monitors/*.yaml   what to watch
```

See [`examples/`](examples) for a working set. Secrets are never written in these files: refer to an
environment variable instead, for example `password: ${PULSE_SMTP_PASSWORD}`.

Every command that prints data accepts `--json`. That output is a versioned contract
(`api_version`), so scripts and tools built on it keep working across releases.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as described in
[SECURITY.md](SECURITY.md).

## Licence

GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
"brynko" and "brynko-pulse" are trademarks; see [TRADEMARKS.md](TRADEMARKS.md).
