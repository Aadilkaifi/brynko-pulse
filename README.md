# brynko-pulse

Open-source monitoring and alerts for Linux and Windows servers, developed by
[brynko technologies](https://brynko.in). One small program watches the server and tells you within
seconds when something breaks, through the channels you choose: email,
WhatsApp (your own Meta account), Telegram, Slack or any webhook.

> **Status: first public release (v0.1).** In daily use on our own Linux and Windows servers. Expect
> rough edges, and please report what you find.

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
pulse status                          # disk, memory, CPU and swap are watched; what install found is listed, inactive
sudoedit /etc/brynko-pulse/monitors/service-nginx.yaml   # remove "enabled: false" to watch it
sudoedit /etc/brynko-pulse/channels.yaml                 # where alerts go
sudo pulse validate                   # says exactly what to fix
sudo pulse test channel ops-email     # sends a test alert
sudo systemctl reload brynko-pulse    # applies the change
```

## Manage every server from one app

pulse is complete on its own. If you look after more than a few servers, the **brynko devOps Agent**
desktop app gives you one place for all of them:

- **Install in one click** on Linux and Windows servers over SSH, checked against the release
  checksums. Nothing to download on the server.
- **Every server at a glance:** what each monitor watches, what it alerts on in plain words, and
  what is failing right now.
- **Forms instead of YAML**, built from pulse itself, with the server's own disks, services,
  containers and ports to pick from, and a check before anything is saved.
- **Channels, silences, maintenance windows and defaults** in a few clicks.
- **An AI assistant** that reads your servers and this guide, explains what is wrong, and makes
  changes only after you approve each one.

The same app also works with your containers, databases, files and deploy scripts.
[Download brynko devOps Agent](https://cloudagent.brynko.in/download) and see
[Using pulse from an app](docs/ui.md).

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
| [Using pulse from an app](docs/ui.md) | The brynko devOps Agent desktop app: install, monitors, channels, settings |
| [Building from source](docs/build.md) | Build, test, release |

Every command that prints data accepts `--json`. That output is a versioned contract
(`api_version`), so scripts and tools built on it keep working across releases.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as described in
[SECURITY.md](SECURITY.md).

## Licence

GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
"brynko" and "brynko-pulse" are trademarks; see [TRADEMARKS.md](TRADEMARKS.md).
