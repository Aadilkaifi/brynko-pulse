# Operations

## Is it working?

```sh
pulse status          # every monitor and channel, failing ones first
pulse history         # recent alerts, newest first (--limit 50 for more)
```

`status` warns when its information is more than a minute old, which means the service is not
running. Monitors turned off with `enabled: false` are listed last as `INACTIVE`. Both accept
`--json`.

## JSON output

Every command that prints data accepts `--json`. The output is a contract that tools can rely on:

- Every answer is one JSON object with `api_version` (now `1`), `command` and `ok`; when `ok` is
  false, `error` says why and the exit code is not 0.
- A new `api_version` is only used for a change that would break existing readers. Fields may be
  added within a version, so ignore the ones you do not know.
- Fields with no value are left out: a time that never happened (no problem yet, never checked) is
  absent rather than empty, and `config show` lists only the settings a monitor or channel has.
- Durations are written as people write them: `30s`, `15m`, `1h30m`.
- In `status --json`, each monitor has `enabled`; an inactive monitor is not checked and has no
  values. A monitor with a heal rule also has `heal` (see [Self-healing](self-healing.md)).
- `history` lists alerts and, as kind `heal`, every fix [self-healing](self-healing.md) tried.

## The service

| | Linux | Windows (Administrator PowerShell) |
|---|---|---|
| State | `systemctl status brynko-pulse` | `Get-Service brynko-pulse` |
| Logs | `journalctl -u brynko-pulse -f` | Event Viewer, Windows Logs, Application, source `brynko-pulse` |
| Reload config | `sudo systemctl reload brynko-pulse` | `sc.exe control brynko-pulse paramchange` |
| Restart | `sudo systemctl restart brynko-pulse` | `Restart-Service brynko-pulse` |
| Stop | `sudo systemctl stop brynko-pulse` | `Stop-Service brynko-pulse` |

The service restarts by itself if it crashes. To run pulse in a terminal instead, for example
while trying a configuration, stop the service and run `sudo pulse run`; stop it with Ctrl+C.

## Heartbeats from jobs

A job reports that it finished with `pulse ping <id>` (see the `heartbeat` monitor in
[Monitors](monitors.md)). On Linux it must run as root or as a member of the `brynko-pulse` group:

```sh
sudo usermod -aG brynko-pulse backupuser
```

## Troubleshooting

| What you see | What to do |
|---|---|
| `refusing to start: ... can be changed by other users` | Run the `chmod` command in the message. Only administrators may change the configuration, because it can run commands |
| `... holds secrets but other users can read or change it` | `sudo chmod 640 /etc/brynko-pulse/secrets.env` |
| `X is not set. Add it to secrets.env` | Add `X=value` to `secrets.env`, then reload |
| A channel shows `last delivery failed` | `sudo pulse test channel <name>` prints the provider's error |
| `cannot reach Docker` | pulse was installed with `--no-docker`; run `sudo pulse install` again without it |
| A log monitor never matches | Check the service user can read the file; `sudo pulse install` warns about unreadable files |
| `the service did not stay running` | `journalctl -u brynko-pulse -n 50` shows why |
| `pulse is not allowed to restart this service` | Run `sudo pulse install` (Windows: `pulse install` as Administrator); it grants the right for monitors with `heal: restart` |
| `heal: restart cannot work on this server: its polkit cannot limit a right` | The server's polkit is older than 0.106. Use `heal: run` with a program, or upgrade the system |
| A monitor says `not running` in status | `sudo pulse validate`; the reason is in the status detail |
