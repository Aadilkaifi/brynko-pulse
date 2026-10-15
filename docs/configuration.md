# Configuration

pulse is configured with plain YAML files in one folder: `/etc/brynko-pulse` on Linux,
`%ProgramData%\brynko-pulse` on Windows. `pulse install` (or `pulse init`) creates it with a
commented starter setup and never overwrites existing files.

```
pulse.yaml        server name, defaults, limits, maintenance windows
channels.yaml     where alerts go
secrets.env       passwords and tokens used by channels.yaml (kept private)
monitors/*.yaml   what to watch; every .yaml file in this folder is read
```

Every field is listed in the [configuration reference](config-reference.md). The files are
checked against a published JSON Schema (`pulse schema` prints it, so editors can use it for
completion).

## Changing the configuration

1. Edit the files, or use `pulse add` and `pulse discover --apply`, which write
   `monitors/<id>.yaml` for you after checking the whole configuration.
2. Check it: `sudo pulse validate`. Errors name the file and line and say what to fix; warnings
   point out what will not work as you expect, such as a secret that is not set.
3. Apply it without a restart: `sudo systemctl reload brynko-pulse` on Linux, or
   `sc.exe control brynko-pulse paramchange` on Windows. A configuration with errors is refused on
   reload and the old one keeps running.

For safety the files must not be changeable by ordinary users, and `secrets.env` must not be
readable by them; pulse refuses to start otherwise and tells you the command that fixes it.

## Changing it from scripts and tools

Tools such as a desktop app change the configuration through pulse, never by writing files
themselves, so every change gets the same checks:

```sh
pulse config show --json                       # everything, with the file each monitor lives in
pulse config get monitors/web.yaml             # one file
sudo pulse config set monitors/web.yaml < web.yaml
sudo pulse config set monitors/web.yaml --dry-run < web.yaml   # check and test, save nothing
sudo pulse config delete monitors/web.yaml
printf '%s' "$PASSWORD" | sudo pulse secret set PULSE_SMTP_PASSWORD
```

`set --dry-run` checks the file as if it were saved and runs each monitor in it once, so a form
can show what the monitor would measure before anything changes. `set` and `delete` refuse a change that would make the configuration invalid and leave the file
as it was. Secrets are read from standard input, so they never appear in the process list, and
`config show` lists their names only, never their values.

## Secrets

Secret fields only accept a reference, `${NAME}`. Put the value in `secrets.env`, one per line:

```sh
PULSE_SMTP_PASSWORD=my-app-password
PULSE_TELEGRAM_TOKEN="123:abc"     # quotes are optional and removed
```

An environment variable with the same name takes precedence, which is handy for a one-off test.
Secrets never appear in logs, errors or `--json` output.

## Defaults

`defaults` in `pulse.yaml` apply to every monitor that does not set its own value:

```yaml
defaults:
  severity: warning     # info, warning or critical
  notify: [ops-email]   # channel names from channels.yaml
  interval: 30s         # how often each monitor is checked
  for: 0s               # how long a problem must last before alerting
  cooldown: 15m         # how often to repeat while it continues
```

## Conditions

`alert_when` and `clear_when` are one comparison: a value the monitor measures, an operator
(`>`, `<`, `>=`, `<=`, `==`, `!=`) and a number, duration, true/false or quoted text:

```yaml
alert_when: used_percent > 85
alert_when: age > 26h
alert_when: running == 0          # 0 is false, 1 is true
alert_when: state == "failed"
```

Each monitor type's values are listed in [Monitors](monitors.md). To see the values a monitor
reports right now: `sudo pulse test monitor <id>`.

## When alerts are sent

- **Wait before alerting:** with `for: 2m`, a problem must last two minutes, so a short spike does
  not wake anyone.
- **Recovery margin:** with `clear_when`, a monitor counts as fixed only when that is true, so it
  does not go on and off around one threshold (alert above 85%, clear below 80%).
- **Repeats:** while a problem continues, the alert repeats every `cooldown`.
- **Storm guard:** above `limits.max_alerts_per_minute` (default 10), pulse sends one summary
  instead of a flood.
- **Flapping:** a monitor that changes between failing and fine 5 times within 30 minutes sends one
  "flapping" alert and then stays quiet until it has been stable for half that time, when it sends
  one "settled" alert. Change it under `defaults.flapping`; `changes: 0` turns it off.
- **Restarts:** pulse remembers what it already sent, so a restart or reload does not send the
  same alert again.

Service, container, file and log monitors are also checked the moment something changes, not only
at each interval.

## Silences

Pause alerts while you work on something:

```sh
sudo pulse silence web 1h --reason "deploy"   # one monitor
sudo pulse silence all 30m                    # every monitor
pulse silence list
sudo pulse silence clear web
```

Monitors keep being checked while silenced, and held alerts appear in `pulse history` marked as
silenced. If a monitor is still failing when the silence ends, you get one alert. A silence lasts
at most 30 days.

## Maintenance windows

For work that happens every week, add a window to `pulse.yaml`. It works exactly like a silence:

```yaml
maintenance:
  - name: weekly-patching
    days: [sun]                 # mon tue wed thu fri sat sun
    from: "02:00"               # 24-hour clock
    to: "03:00"                 # earlier than from means it runs past midnight
    timezone: Asia/Kolkata      # optional; default is the server's local time
    monitors: [web, db]         # optional; default is every monitor
```

A window that runs past midnight belongs to the day it starts.
