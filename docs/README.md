# brynko-pulse documentation

| Page | What it covers |
|---|---|
| [Install](install.md) | Installing on Linux and Windows, upgrading, uninstalling |
| [Configuration](configuration.md) | The config files, secrets, conditions, silences, maintenance windows |
| [Monitors](monitors.md) | Every monitor type: what it measures and how to alert on it |
| [Channels](channels.md) | Every alert channel: email, WhatsApp, Telegram, Slack, webhook |
| [Operations](operations.md) | Day-to-day use: status, history, logs, reloading, troubleshooting |
| [Command reference](cli.md) | Every `pulse` command and flag (generated) |
| [Configuration reference](config-reference.md) | Every config field (generated from the schema) |
| [Building from source](build.md) | Building, testing, regenerating these docs, releasing |

Two pages are generated from the code, so they always match the version you run:
[cli.md](cli.md) from the command table and [config-reference.md](config-reference.md) from the
JSON Schema. The tests fail if either is out of date.
