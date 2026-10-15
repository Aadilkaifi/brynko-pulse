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
| `api_key` | secret `${NAME}` | yes |  |  |
| `email` | list of email addresses |  |  |  |
| `sms` | list of phone numbers (+919876543210) |  |  |  |
| `whatsapp` | list of phone numbers (+919876543210) |  |  |  |

At least one of the delivery lists above is required.

## monitors/*.yaml

Each file has a `monitors` list. Every monitor has:

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `id` | id | yes |  | Unique name, used by status, history, silences and ping. |
| `type` | `cpu` \| `memory` \| `load` \| `swap` \| `disk` \| `service` \| `container` \| `process` \| `port` \| `http` \| `ssl` \| `log` \| `file` \| `command` \| `heartbeat` | yes |  | What to watch; each type adds its own fields below. |
| `alert_when` | condition (`metric > value`) |  |  | The comparison that means something is wrong. Some types have a default. |
| `clear_when` | condition (`metric > value`) |  |  | When it counts as fixed again, to avoid alerts going on and off around a threshold. |
| `cooldown` | duration (30s, 5m, 2h, 1d) |  |  | How often to repeat while it keeps failing. Default: defaults.cooldown. |
| `enabled` | boolean |  | `true` | false keeps the monitor but stops checking it. |
| `for` | duration (30s, 5m, 2h, 1d) |  |  | How long alert_when must hold before alerting. |
| `interval` | duration (30s, 5m, 2h, 1d) |  |  | How often to check. Default: defaults.interval. |
| `name` | string |  |  | Name shown in alerts. Default: the id. |
| `notify` | list of channel names |  |  | Channels for this monitor. Default: defaults.notify. |
| `severity` | `info` \| `warning` \| `critical` |  |  | Default: defaults.severity. |

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
| `path` | string | yes |  |  |

### Monitor type `service`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `service` | string | yes |  |  |

### Monitor type `container`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `container` | string | yes |  |  |

### Monitor type `process`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `match` | string | yes |  |  |

### Monitor type `port`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `port` | integer | yes |  |  |
| `host` | string |  | `127.0.0.1` |  |
| `timeout` | duration (30s, 5m, 2h, 1d) |  | `5s` |  |

### Monitor type `http`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `url` | string | yes |  | Must start with http:// or https://. |
| `body_contains` | string |  |  |  |
| `expect_status` | integer or list of integer |  |  |  |
| `follow_redirects` | boolean |  | `true` |  |
| `method` | `GET` \| `HEAD` \| `POST` |  | `GET` |  |
| `timeout` | duration (30s, 5m, 2h, 1d) |  | `10s` |  |

### Monitor type `ssl`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `file` | string |  |  |  |
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
| `path` | string | yes |  |  |

### Monitor type `command`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `run` | list of string | yes |  | The program and its arguments. Never run through a shell. |
| `parse` | `exit_code` \| `text` \| `number` |  | `exit_code` |  |
| `timeout` | duration (30s, 5m, 2h, 1d) |  | `30s` |  |

### Monitor type `heartbeat`

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `expect_every` | duration (30s, 5m, 2h, 1d) | yes |  |  |
| `grace` | duration (30s, 5m, 2h, 1d) |  | `1m` |  |
