<!-- Generated from schema/pulse.schema.json. Do not edit: run `go test ./cmd/pulse ./schema -run Docs -update`. -->

# Configuration reference

Every field pulse accepts, generated from the published JSON Schema (`pulse schema`). For a guided
introduction see [configuration.md](configuration.md). Unknown fields are errors, so a typo is
caught by `pulse validate` instead of being ignored.

## pulse.yaml

### Top level

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `server_name` | name (letters, digits, `.` `_` `-`) | yes |  |  |
| `version` | `1` | yes |  |  |
| `api` | [api](#api) |  |  |  |
| `defaults` | [defaults](#defaults) |  |  |  |
| `limits` | [limits](#limits) |  |  |  |
| `maintenance` | list of [maintenance](#maintenance) windows |  |  |  |
| `server_address` | string |  |  | How people reach this server, shown in alerts beside server_name, for example 203.0.113.10 or web-1.example.com. |
| `watchdog` | [watchdog](#watchdog) |  |  |  |

### defaults

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `cooldown` | duration (30s, 5m, 2h, 1d) |  | `15m` | How often to repeat an alert while the problem continues. |
| `flapping` | [flapping](#flapping) |  |  | Flapping detection. |
| `for` | duration (30s, 5m, 2h, 1d) |  |  | How long a problem must last before alerting. |
| `interval` | duration (30s, 5m, 2h, 1d) |  | `30s` | How often each monitor is checked. |
| `notify` | list of channel names |  |  | Channels for monitors that set none. |
| `severity` | `info` \| `warning` \| `critical` |  | `warning` | How urgent alerts are. |

### flapping

A monitor that changes state this many times within the window is marked flapping and sends one alert instead of many.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `changes` | integer |  | `5` | 0 turns flapping detection off. |
| `window` | duration (30s, 5m, 2h, 1d) |  | `30m` |  |

### limits

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `command_output_bytes` | integer |  | `65536` | Output kept from a command check. |
| `max_alerts_per_minute` | integer |  | `10` | Above this, alerts are folded into one summary message. |
| `max_heals_per_hour` | integer |  | `10` | Self-healing attempts allowed per hour across all monitors on this server. Above this, pulse stops trying and alerts. |

### api

Local API for tools and a future UI. Never listens on a network port.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | boolean |  | `false` |  |

### maintenance

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `days` | list of `mon` \| `tue` \| `wed` \| `thu` \| `fri` \| `sat` \| `sun` | yes |  | Days the window starts on. |
| `from` | time `HH:MM` | yes |  |  |
| `name` | id | yes |  |  |
| `to` | time `HH:MM` | yes |  |  |
| `monitors` | list of id |  |  | Monitor ids the window applies to. Default: every monitor. |
| `timezone` | string |  |  | An IANA time zone such as Asia/Kolkata. Default: the server's local time. |

## channels.yaml

`channels` maps a name you choose (an id) to a channel. Every channel has:

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | `smtp` \| `whatsapp` \| `telegram` \| `slack` \| `webhook` \| `brynko` | yes |  | Where the alert goes; each type adds its own fields below. |
| `min_severity` | `info` \| `warning` \| `critical` |  | `info` | Send only alerts at or above this severity. |

### Channel type `smtp`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `from` | string | yes |  |  |
| `host` | string | yes |  |  |
| `to` | list of email addresses | yes |  |  |
| `password` | secret `${NAME}` |  |  |  |
| `port` | integer |  | `587` |  |
| `tls` | `starttls` \| `tls` \| `none` |  | `starttls` |  |
| `username` | string |  |  |  |

### Channel type `whatsapp`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `access_token` | secret `${NAME}` | yes |  |  |
| `phone_number_id` | string | yes |  |  |
| `template` | string | yes |  |  |
| `to` | list of phone numbers (+919876543210) | yes |  |  |
| `language` | string |  | `en` | The template's language code, for example en or en_US. |

### Channel type `telegram`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `bot_token` | secret `${NAME}` | yes |  |  |
| `chat_id` | string | yes |  |  |

### Channel type `slack`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `webhook_url` | secret `${NAME}` | yes |  |  |

### Channel type `webhook`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `url` | string | yes |  | Must start with http:// or https://. |
| `secret` | secret `${NAME}` |  |  |  |
| `timeout` | duration (30s, 5m, 2h, 1d) |  | `10s` |  |

### Channel type `brynko`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `api_key` | secret `${NAME}` | yes |  | This server's brynko key, as a ${NAME} reference to secrets.env. The app sets it up for you. |
| `email` | list of email addresses |  |  | Email addresses. |
| `url` | string |  | `https://devops.brynko.in/api/v1/pulse/alert` | Where the relay takes alerts: an https:// address. Leave it unset. |
| `whatsapp` | list of phone numbers (+919876543210) |  |  | WhatsApp numbers in international format, for example +919876543210. |

At least one of the delivery lists above is required.

## monitors/*.yaml

Each file has a `monitors` list. Every monitor has:

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `id` | id | yes |  | Unique name, used by status, history, silences and ping. |
| `type` | `cpu` \| `memory` \| `load` \| `swap` \| `disk` \| `service` \| `container` \| `process` \| `port` \| `http` \| `ssl` \| `log` \| `file` \| `command` \| `heartbeat` | yes |  | What to watch; each type adds its own fields below. |
| `alert_when` | condition (`metric > value`) |  |  | The comparison that means something is wrong. Some types have a default. |
| `clear_when` | condition (`metric > value`) |  |  | When the problem counts as over, and pulse sends a resolved message. Set a lower level than the alert (alert above 85%, back to normal below 80%) so it does not flip back and forth around one value. Empty: as soon as alert_when is no longer true. |
| `cooldown` | duration (30s, 5m, 2h, 1d) |  |  | While the problem lasts, the alert is sent again this often, until it is over. Empty: the server's default (defaults.cooldown). |
| `enabled` | boolean |  | `true` | Off keeps the monitor in its file but stops checking it. |
| `for` | duration (30s, 5m, 2h, 1d) |  |  | How long the problem must last before you are alerted, so a short spike does not wake anyone. Empty: the server's default (defaults.for), or at once when it sets none. |
| `heal` | [heal](#heal) |  |  |  |
| `interval` | duration (30s, 5m, 2h, 1d) |  |  | How often pulse measures it. Types that follow events (containers, services, files, logs) also notice a change at once; this is the regular check in between. Empty: the server's default (defaults.interval). |
| `name` | string |  |  | Shown in alerts and lists. Empty: the id. |
| `notify` | list of channel names |  |  | Channels told about this monitor. Empty: the server's default channels (defaults.notify). |
| `severity` | `info` \| `warning` \| `critical` |  |  | How serious an alert is. Empty: the server's default (defaults.severity). |

### Monitor type `cpu`

No extra fields.

Requires `alert_when`.

### Monitor type `memory`

No extra fields.

Requires `alert_when`.

### Monitor type `load`

No extra fields.

Requires `alert_when`.

### Monitor type `swap`

No extra fields.

Requires `alert_when`.

### Monitor type `disk`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | yes |  | A filesystem's mount point, or any path on it, for example / or /data (on Windows D:\). |

### Monitor type `service`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `service` | string | yes |  | The service name, for example nginx: on Linux the systemd unit without .service, on Windows the service name. |

### Monitor type `container`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `container` | string | yes |  | The container name, as docker ps shows it. |

### Monitor type `process`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `match` | string | yes |  | The process name exactly as ps shows it, for example nginx. On Linux a process whose command line contains it also counts. |

### Monitor type `port`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `port` | integer | yes |  | The TCP port, for example 443. |
| `host` | string |  | `127.0.0.1` | Where to connect. Empty: this server (127.0.0.1). |
| `timeout` | duration (30s, 5m, 2h, 1d) |  | `5s` | Give up after this long. |

### Monitor type `http`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `url` | string | yes |  | Must start with http:// or https://. |
| `body_contains` | string |  |  | Text the answer must contain. pulse reads the answer as text (up to 1 MB) and looks for this exactly as written, capitals included, for example "status":"ok". |
| `expect_status` | integer or list of integer |  |  | Status codes that count as up, for example 200, or 200 and 301. Empty: any 2xx or 3xx. |
| `follow_redirects` | boolean |  | `true` | Follow redirects to the final page. |
| `json_expect` | string |  |  | The value the JSON field must have, as a comparison: == ok, != error, > 0. When set, a different value counts as not answering as expected. Text is compared with == or != only; quote a number to compare it as text. |
| `json_path` | string |  |  | For a JSON answer: the field to read, for example status, data.state or items.0.name (a number picks a list item). Alert on its value with json_value (a number or text), or on whether it is there with json_found. |
| `method` | `GET` \| `HEAD` \| `POST` |  | `GET` | GET for pages and most APIs. |
| `timeout` | duration (30s, 5m, 2h, 1d) |  | `10s` | Give up after this long. |

### Monitor type `ssl`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `file` | string |  |  | Or a certificate file on this server, for example /etc/ssl/certs/site.pem. |
| `host` | string |  |  | A host name with an optional port, for example example.com:443. |

Exactly one of `host`, `file`.

### Monitor type `log`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `pattern` | string | yes |  | A regular expression; a line that contains a match counts. |
| `source` | object | yes |  | Exactly one of: file (a path), journal (a systemd unit, or empty for the whole journal), eventlog (a Windows channel such as Application). |
| `window` | duration (30s, 5m, 2h, 1d) |  | `1m` | matches counts the lines that matched within this time. |

### Monitor type `file`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `path` | string | yes |  | The file to watch, for example /var/backups/db.sql.gz. |

### Monitor type `command`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `run` | list of string | yes |  | The program and its arguments. Never run through a shell. |
| `parse` | `exit_code` \| `text` \| `number` |  | `exit_code` | What to read: only the exit code, the output as text, or the output as a number (it must start with one). |
| `timeout` | duration (30s, 5m, 2h, 1d) |  | `30s` | Give up after this long. |

### Monitor type `heartbeat`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `expect_every` | duration (30s, 5m, 2h, 1d) | yes |  | How often the job runs, for example 1d for a nightly job. |
| `grace` | duration (30s, 5m, 2h, 1d) |  | `1m` | Extra time allowed before it counts as late. |

## Monitor fields with their own section

### heal

Self-healing: what pulse does when this monitor fails. In ask mode pulse prepares the fix and waits for you to approve it; in auto mode it runs the fix by itself. Either way it waits until the problem has lasted the monitor's for (at least 30s), runs the steps in order, and waits verify for the monitor to be OK again.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `steps` | list of [healStep](#healstep) | yes |  | What to do, in order. pulse stops at the first step that fails. |
| `attempts` | integer |  | `3` | How many automatic fixes within the window. Fixes you approve are not counted. |
| `mode` | `off` \| `ask` \| `auto` |  | `ask` | ask: prepare the fix and wait for approval (pulse fix approve, or the app). auto: run it by itself; after 2 automatic fixes that did not help in one problem, or when the tries are used up, the next fix waits for approval. off: keep the rule but do nothing. |
| `only_if_ok` | list of id |  |  | Monitors that must be OK before a fix. When one of them is failing, it is the likely cause: pulse does not fix this one and says so in the alert. |
| `timeout` | duration (30s, 5m, 2h, 1d) |  | `90s` | How long all the steps together may take. |
| `verify` | duration (30s, 5m, 2h, 1d) |  | `2m` | How long to wait after a fix for the monitor to be OK again before counting it as not helping. |
| `within` | duration (30s, 5m, 2h, 1d) |  | `1h` | The window automatic fixes are counted in. |

### healStep

One step: restart services, restart containers, or run a program.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `restart_container` | list of string |  |  | Docker containers to restart, in order, by name as docker ps shows it. |
| `restart_service` | list of string |  |  | Services to restart, in order: on Linux systemd units without .service, on Windows service names. pulse gets the right to restart only these. |
| `run` | list of string |  |  | A program and its arguments. Never run through a shell; it runs as the pulse service account. |

Each step has exactly one of `restart_service`, `restart_container` or `run`. `ssl`, `file` and `heartbeat` monitors cannot have `heal`. See [self-healing.md](self-healing.md).
