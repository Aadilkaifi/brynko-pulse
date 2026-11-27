# Operations

## Is it working?

```sh
pulse status          # every monitor and channel, failing ones first
pulse history         # recent alerts, newest first (--limit 50 for more)
```

`status` warns when its information is more than a minute old, which means the service is not
running. Monitors turned off with `enabled: false` are listed last as `INACTIVE`. Both accept
`--json`.

## Trends

```sh
pulse trends                # the last 24 hours: CPU, memory, swap, load, and each disk
pulse trends --since 7d     # a week, from 15-minute averages
```

While it runs, pulse records CPU, memory, swap, load (Linux) and disk use once a minute. It keeps
every reading for a day, 15-minute averages for 14 days and hourly averages for 90 days, in the
`trends` folder of the data folder; that is a few MB in all. The disks recorded are the system disk
and every disk a `disk` monitor watches. The summary shows each value now, on average and at its
highest, and how much each disk grew.

When a disk has grown steadily for at least a day, `trends` says when it will be full at that rate,
if that is within 90 days, for example: `/data fills up in about 5 days (around 2 Dec)`. Growth that
is too uneven to project is not guessed at, and a trend starts again after space is freed.

## Changes

```sh
pulse changes                     # what changed in the last 24 hours, newest first
pulse changes --since 7d --kind sudo,file
```

While it runs, pulse keeps a timeline of what changed on the machine, so "what changed before this
broke?" has an answer:

| Kind | What is recorded | Read from |
|---|---|---|
| `sudo` | Each command run with sudo, who ran it, as whom and in which folder | systemd journal (Linux) |
| `login` | Each SSH sign-in: who, from where, with a key or a password | systemd journal (Linux) |
| `package` | Packages installed, upgraded or removed, and by whom; Windows software and updates | apt history (Debian, Ubuntu); Event Log (Windows) |
| `service` | A service installed, or its start type changed | Event Log (Windows) |
| `reboot` | The machine started; on Windows also who started a restart or shutdown | uptime; Event Log (Windows) |
| `container` | A container created, removed, renamed, its settings changed, stopped, restarted or paused; an image pulled or deleted | Docker (Linux) |
| `file` | A configuration file changed, appeared in a watched folder or was removed, with the lines that changed | fingerprints every 30 seconds |

The configuration files watched are common ones (`/etc/hosts`, `/etc/fstab`, cron, sshd, nginx,
Apache, Docker, systemd units, MySQL, Redis, PostgreSQL; on Windows the hosts file and the IIS
configuration) and the compose file of every container. When someone ran a sudo command naming a
file shortly before it changed, the change says who.

A common file the service may not read (some are readable only by their own service) is skipped.
A compose file it cannot read is reported, because it is usually the one you care about: often it
sits in a home folder the service cannot enter. `pulse changes` then names the folder and the
command that lets the service pass through it without listing it, for example:

```sh
sudo setfacl -m u:brynko-pulse:x /home/ubuntu
```

Below the timeline, `pulse changes` lists anything it cannot record right now and why: no access to
the journal (run `sudo pulse install` again), Docker not reachable, or compose files it cannot read.

Some things to know:

- **Passwords and keys are hidden.** Values whose name says they are secret (`DB_PASSWORD=`,
  `password:`, `--token`) are shown as `****` in commands and diffs. Files whose name says they hold
  secrets (`.env`, `*.key`, `*.pem`) are fingerprinted only: the change is listed, the contents
  never. pulse keeps a masked copy of each other watched file to show what changed.
- **Repeats are listed once an hour.** The same change by the same person (a tool that signs in every
  minute, the same command run again) is recorded once within an hour, so the timeline stays
  readable. It is a timeline for finding causes, not a complete audit log.
- **A first start looks back a day.** sudo commands, sign-ins and packages from the last 24 hours are
  read when pulse first starts; after that each source continues where it stopped, so changes made
  while pulse was stopped are still found. On Windows the Event Log is read from the moment pulse
  starts.
- Changes are kept for 90 days in the `changes` folder of the data folder, at most 20 MB.

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
- `status --json` has `watchdog` when the [dead-server alert](dead-server-alert.md) is on.
- `history` lists alerts and, as kind `heal`, every fix [self-healing](self-healing.md) tried, approved
  or dismissed.
  `channels` on an alert are the channels it was handed to. One a channel could not deliver after
  its retries has a second record of kind `undelivered`: `channels` names that channel, `detail` is
  its error, and `alert_kind` and `alert_at` are the kind and time of the alert it belongs to.
- `trends --json` has `level` (`1m`, `15m` or `1h`: the detail shown), `since`, `points` (oldest first;
  an average has `samples` above 1 and `cpu_max` / `mem_max`; a value the system does not give, such as
  `load1` on Windows, is absent) and `forecasts` (`disk`, `full_at`, `days_left`, `growth_per_day` and
  `free` in bytes, `confidence` from 0 to 1, `based_on_days`), soonest first.
- `changes --json` has `since`, `changes` (newest first) and `problems`. Each change has `at`, `kind` (`sudo`,
  `login`, `package`, `service`, `reboot`, `container` or `file`), `what` (one line), `source` (`journal`,
  `apt`, `eventlog`, `docker`, `file` or `boot`), and `who` and `detail` when known. `problems` maps
  each source that records nothing right now to the reason (empty when all work).

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
| `cannot reach Docker` | pulse was installed with `--no-docker`; run `sudo pulse install --docker` |
| A log monitor never matches | Check the service user can read the file; `sudo pulse install` warns about unreadable files |
| `the service did not stay running` | `journalctl -u brynko-pulse -n 50` shows why |
| `pulse is not allowed to restart this service` | Run `sudo pulse install` (Windows: `pulse install` as Administrator); it grants the right for the services in `restart_service` heal steps |
| `pulse did not answer` from `pulse fix` | The service is not running, or is older than the command: check `pulse status`, and upgrade pulse |
| `restart_service heal steps cannot work on this server: its polkit cannot limit a right` | The server's polkit is older than 0.106. Use a `run` step with a program, or upgrade the system |
| A monitor says `not running` in status | `sudo pulse validate`; the reason is in the status detail |
