# brynko-pulse

Open-source monitoring and alerts for Linux and Windows servers. One small program watches the
server and tells you within seconds when something breaks, through the channels you choose: email,
WhatsApp (your own Meta account), Telegram, Slack or any webhook.

> **Status: early development.** Not ready for production yet.

## Why

- **Runs on the server itself.** No account, no cloud service, no telemetry.
- **Outbound only.** It never opens a network port.
- **Plain files.** Configuration is YAML, checked against a published JSON Schema, so mistakes are
  caught with a clear message before anything runs.
- **Quiet when it should be.** Alerts wait for a problem to last, recover with a margin, catch
  flapping, and fold storms into one message, so you trust the ones you get.
- **Fast.** Service, container, file and log changes are noticed as they happen.

## Install

```sh
curl -fsSL https://github.com/Aadilkaifi/brynko-pulse/releases/latest/download/install.sh | sudo sh
```

Or install the `.deb` or `.rpm` from the releases page, or run the Windows binary with
`pulse install` as Administrator. See [Install](docs/install.md).

## First steps

```sh
pulse status                          # install already watches the services and containers it found
sudoedit /etc/brynko-pulse/channels.yaml
sudo pulse validate                   # says exactly what to fix
sudo pulse test channel ops-email     # sends a test alert
sudo systemctl reload brynko-pulse    # applies the change
```

## Documentation

| | |
|---|---|
| [Install](docs/install.md) | Linux and Windows, upgrade, uninstall |
| [Configuration](docs/configuration.md) | Files, secrets, conditions, silences, maintenance windows |
| [Monitors](docs/monitors.md) | Disk, memory, CPU, services, containers, ports, websites, certificates, logs, files, commands, heartbeats |
| [Channels](docs/channels.md) | Email, WhatsApp, Telegram, Slack, webhook |
| [Operations](docs/operations.md) | Status, history, logs, troubleshooting |
| [Command reference](docs/cli.md) | Every command and flag |
| [Configuration reference](docs/config-reference.md) | Every config field |
| [Building from source](docs/build.md) | Build, test, release |

Every command that prints data accepts `--json`. That output is a versioned contract
(`api_version`), so scripts and tools built on it keep working across releases.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as described in
[SECURITY.md](SECURITY.md).

## Licence

GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
"brynko" and "brynko-pulse" are trademarks; see [TRADEMARKS.md](TRADEMARKS.md).
