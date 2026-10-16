# Monitors

A monitor measures one thing and produces named **values**. Its `alert_when` condition compares one
value, for example `used_percent > 85`. Many types have a default condition, so the shortest monitor
is just an id and a type. Every field of every type is listed in the
[configuration reference](config-reference.md).

The quickest way to add one is `pulse add` (see `pulse add -h`), or let `pulse discover` suggest
monitors for what runs on the server. Tools read the same list of types and values from
`pulse schema` (`x-types`), and what the server has to watch from `pulse suggest`. To see what a monitor measures right now:

```sh
sudo pulse test monitor <id>
```

Conditions compare numbers (`> < >= <= == !=`), durations (`age > 1d`), true/false values
(`running == 0` means not running) and text (`state == "failed"`). See
[Configuration](configuration.md#conditions).

## Fields every monitor has

| Field | What it does |
|---|---|
| `id` | Unique name, used by `status`, `history`, silences and `ping` |
| `type` | One of the types below |
| `name` | Shown in alerts and lists; the id when empty |
| `enabled` | `false` keeps the monitor in its file without checking it |
| `alert_when` | The comparison that means something is wrong; many types have a default |
| `clear_when` | When the problem counts as over; by default when `alert_when` is no longer true |
| `for` | How long a problem must last before alerting |
| `severity` | `info`, `warning` or `critical` |
| `notify` | Channels to tell; `defaults.notify` when empty |
| `cooldown` | How often to repeat the alert while the problem lasts |
| `interval` | How often to measure; changes to services, containers, files and logs are also noticed at once |

Empty fields take the values under `defaults` in `pulse.yaml` (see
[Configuration](configuration.md#defaults)).

## `disk`

Space on the filesystem that holds `path`.

- **Values:** `used_percent`, `free_gb`, `free_bytes`, `total_bytes`, and on Linux `inodes_used_percent`.
- **No default condition.**

```yaml
- id: data-disk
  type: disk
  path: /data            # on Windows: 'D:\'
  alert_when: used_percent > 85
  clear_when: used_percent < 80
```

## `memory`

- **Values:** `used_percent`, `available_gb`, `available_bytes`, `total_bytes`.
- **No default condition.** Example: `alert_when: used_percent > 90`.

## `swap`

Swap on Linux, the page file on Windows.

- **Values:** `used_percent`, `used_bytes`, `total_bytes`.
- **No default condition.** Example: `alert_when: used_percent > 80`.

## `cpu`

Usage across all cores, measured between two checks.

- **Values:** `used_percent`.
- **No default condition.** Example: `alert_when: used_percent > 95` with `for: 5m`.

## `load`

The load average (Linux only).

- **Values:** `load1`, `load5`, `load15`, `per_cpu` (`load1` divided by the number of cores).
- **No default condition.** Example: `alert_when: per_cpu > 2`.

## `service`

A systemd unit on Linux, a Windows Service on Windows. Changes are noticed as they happen.

- **Values:** `active`, `state` (for example `running`, `failed`, `stopped`), and on Linux `restarts`.
- **Default condition:** `active == 0`.

```yaml
- id: web
  type: service
  service: nginx
```

## `process`

Processes whose name equals `match` (or, on Linux, whose command line contains it).

- **Values:** `count`.
- **Default condition:** `count == 0`.

## `container`

A Docker container, Linux only. Container events are noticed as they happen. `pulse install`
gives the service Docker access unless you install with `--no-docker` (see [Install](install.md)).

- **Values:** `running`, `state`, `restarts`, `exit_code`, `oom_killed`, and `health` when the
  container has a health check.
- **Default condition:** `running == 0`.

## `port`

Whether a TCP port accepts connections. pulse connects and closes at once, without speaking the
service's protocol. Web servers accept that quietly, but databases, SSH and brokers (MySQL,
PostgreSQL, Redis, MongoDB and similar) log each such connection as a failed client, and MySQL can
even block the address after enough of them. Watch those with a `service` or `container` monitor
instead. `pulse discover` suggests port checks only for web ports (80 and 443).

- **Values:** `open`, `latency_ms`.
- **Default condition:** `open == 0`. `host` defaults to `127.0.0.1`.

## `http`

A web address. Redirects are followed unless `follow_redirects: false`.

- **Values:** `ok`, `status`, `latency_ms`, `body_match` (when `body_contains` is set),
  `json_found` and `json_value` (when `json_path` is set), `json_match` (when `json_expect` is set),
  `error`.
- **Default condition:** `ok == 0`. `ok` means the status is 2xx or 3xx, or one of
  `expect_status`, the body contains `body_contains` when set, and the JSON field matches
  `json_expect` when set.

To check what an API answers, give `json_path`: the field to read, with dots between keys and a
number for a list item (`status`, `data.state`, `items.0.name`), and `json_expect`: the value it
must have, as a comparison (`== ok`, `!= error`, `> 0`). A number is compared as a number, text with
`==` or `!=` only. Quote the whole comparison when it starts with `>` or `<`
(`json_expect: "> 0"`): YAML reads a bare `>` as the start of a text block. The default condition then alerts on a wrong status, a missing field or another
value alike, and the alert says which (`JSON field is "degraded"`):

```yaml
- id: api-health
  type: http
  url: https://api.example.com/health
  json_path: data.status
  json_expect: == ok
```

Without `json_expect`, the value is still read: `json_value` (a number or text) and `json_found`
can be used in `alert_when` like any other value.

```yaml
- id: site
  type: http
  url: https://example.com/health
  expect_status: 200
  body_contains: '"status":"ok"'
```

## `ssl`

A TLS certificate, from a server (`host`, port 443 by default) or a file (`file`).

- **Values:** `days_left`, `valid`.
- **Default condition:** `days_left < 14`.

## `log`

Lines that match `pattern` (a regular expression) in a log, counted over `window` (default 1m).

- **Sources:** `file: /var/log/app.log` (followed from its end; rotation is handled),
  `journal: nginx` (a systemd unit; empty means the whole journal), `eventlog: Application`
  (a Windows Event Log channel; `Security` needs Administrator rights).
- **Values:** `matches`, `last_line`.
- **Default condition:** `matches > 0`.

```yaml
- id: app-errors
  type: log
  source: { file: /var/log/myapp/error.log }
  pattern: "ERROR|FATAL"
  window: 1m
  alert_when: matches > 20
```

For Windows events, the line matched is `provider=<name> id=<event id> level=<level> <data>`, so
`pattern: "id=7034"` catches a service that stopped unexpectedly.

## `file`

A file's age and size. Changes are noticed within two seconds.

- **Values:** `exists`, `age` (seconds since it changed), `size_bytes`.
- **No default condition.** Example: `alert_when: age > 26h` for a nightly backup.

## `command`

Runs a program you choose. `run` is a list (program first, then arguments) and is never passed
through a shell. Output is capped and the command has a timeout.

On Linux the program always runs as the `brynko-pulse` account, with a minimal environment, even
when you test it with `sudo pulse test monitor` or `sudo pulse config set --dry-run`. Give that
account any rights the check needs, rather than running pulse as root. On Windows the service runs
checks as its own account, and `pulse test` runs them as the user who started it.

- **Values:** `exit_code`; with `parse: text` also `text`; with `parse: number` also `value` (the
  output must start with a number).
- **Default condition:** `exit_code != 0`.

```yaml
- id: queue-depth
  type: command
  run: ["/usr/local/bin/queue-depth"]
  parse: number
  alert_when: value > 1000
```

## `heartbeat`

Alerts when a job stops checking in. The job runs `pulse ping <id>` each time it finishes.

- **Values:** `age` (seconds since the last ping), `late`.
- **Default condition:** `late == 1`: no ping for longer than `expect_every` plus `grace`.

```yaml
- id: nightly-backup
  type: heartbeat
  expect_every: 1d
  grace: 1h
```

```sh
# at the end of the backup script, run as root or as a member of the brynko-pulse group
pulse ping nightly-backup
```
