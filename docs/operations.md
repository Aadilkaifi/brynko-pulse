# Operations

## Is it working?

```sh
pulse status          # every monitor and channel, failing ones first
pulse history         # recent alerts, newest first (--limit 50 for more)
```

`status` warns when its information is more than a minute old, which means the service is not
running. Both accept `--json`.

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
| A monitor says `not running` in status | `sudo pulse validate`; the reason is in the status detail |
