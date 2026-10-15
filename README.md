# brynko-pulse

Open-source monitoring and alerts for Linux and Windows servers. One small program watches the
server and tells you within seconds when something breaks, through the channels you choose: email,
WhatsApp (your own Meta account), Telegram, Slack or any webhook.

> **Status: early development.** Not ready for production yet. `pulse` can already watch a server,
> send alerts and run as a background service.

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
sudo ./pulse install     # creates the config if needed, then runs pulse as a service
pulse status             # what is failing right now
pulse history            # recent alerts
```

On Windows, run the same commands in a terminal opened with **Run as administrator**.

`pulse install` creates the configuration in `/etc/brynko-pulse` on Linux or
`%ProgramData%\brynko-pulse` on Windows, and never overwrites existing files:

```
pulse.yaml        server name and defaults
channels.yaml     where alerts go
secrets.env       passwords and tokens for your channels (kept private)
monitors/*.yaml   what to watch
```

Then add a channel and check it:

```sh
sudo pulse validate                  # checks the configuration and says what to fix
sudo pulse test channel ops-email    # sends a test alert
sudo systemctl reload brynko-pulse   # applies changes (Windows: sc.exe control brynko-pulse paramchange)
```

See [`examples/`](examples) for a working set. Secrets are never written in the YAML files: write
`password: ${PULSE_SMTP_PASSWORD}` and put `PULSE_SMTP_PASSWORD=...` in `secrets.env`.

### Finding what to monitor

```sh
sudo pulse discover          # lists running services, containers and ports not monitored yet
sudo pulse discover --apply  # saves them all, one file per monitor
```

### Adding monitors

```sh
sudo pulse add disk /data --above 85
sudo pulse add service nginx --notify ops-email
sudo pulse add http https://example.com/health --status 200
sudo pulse add heartbeat --id nightly-backup --every 1d
sudo pulse add command --id queue-ok -- /usr/local/bin/check-queue
```

Each monitor is saved as its own file, `monitors/<id>.yaml`, only after the whole configuration is
checked, and the running service picks it up. Add `--dry-run` to see the file without saving it.
Run `pulse add -h` for every type and option.

### Silencing alerts during maintenance

```sh
sudo pulse silence web 1h --reason "deploy"   # one monitor
sudo pulse silence all 30m                    # every monitor
pulse silence list
sudo pulse silence clear web
```

Monitors keep being checked while silenced. If one is still failing when the silence ends, you get
one alert, so a problem left behind by maintenance is never missed.

### Maintenance windows

For a weekly window, add `maintenance` to `pulse.yaml` (see the commented sample there): days,
`from` and `to` times, an optional time zone and an optional list of monitors. Alerts are held
back during the window exactly as with `pulse silence`.

### What pulse notices at once

Service changes (systemd on Linux, the Service Control Manager on Windows), container events
(Docker), changed files and new log lines are checked as they happen, not only at each interval.
Log monitors follow a file, the systemd journal or a Windows Event Log channel and count matching
lines (`matches`) over a `window`. A monitor that keeps switching between failing and fine sends
one "flapping" alert instead of one per change.

### Running as a service

- **Linux:** a systemd unit named `brynko-pulse`, running as its own `brynko-pulse` user with no
  extra rights. It can read the configuration and write only its data folder. Logs:
  `journalctl -u brynko-pulse -f`.
- **Windows:** a Windows Service named `brynko-pulse`, running as the `NT SERVICE\brynko-pulse`
  virtual account. Logs are in Event Viewer, under Windows Logs, Application.
- **Container monitors** need Docker access, which is equivalent to root. Grant it only if you use
  them: `sudo pulse install --allow-docker`.
- **Heartbeats:** jobs can run `pulse ping <id>` as root, or as any user in the `brynko-pulse` group.
- `pulse uninstall` removes the service and keeps your configuration and history.
  `pulse uninstall --purge` deletes those too, after asking.

Running `pulse install` again updates the installed binary and service in place.

Every command that prints data accepts `--json`. That output is a versioned contract
(`api_version`), so scripts and tools built on it keep working across releases.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as described in
[SECURITY.md](SECURITY.md).

## Licence

GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
"brynko" and "brynko-pulse" are trademarks; see [TRADEMARKS.md](TRADEMARKS.md).
