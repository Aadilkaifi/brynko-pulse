<!-- Generated from the pulse command table. Do not edit: run `go test ./cmd/pulse -run Docs -update`. -->

# Command reference

Every command prints this help with `pulse <command> -h`. Paths below are the Linux defaults; on
Windows they are `%ProgramData%\brynko-pulse` and `%ProgramData%\brynko-pulse\data`.

Every command that prints data accepts `--json`; that output is a versioned contract (`api_version`).

## Set up

- [`pulse init`](#pulse-init): Create the configuration folder with a starter setup
- [`pulse validate`](#pulse-validate): Check the configuration and explain any problems
- [`pulse discover`](#pulse-discover): Find services, containers and ports to monitor, and suggest monitors
- [`pulse suggest`](#pulse-suggest): List what a monitor could watch here: filesystems, services, containers, ports
- [`pulse add`](#pulse-add): Add a monitor with one command
- [`pulse config`](#pulse-config): Show or change the configuration, one file at a time
- [`pulse secret`](#pulse-secret): Store or remove a secret used by channels
- [`pulse test`](#pulse-test): Send a test alert, or run one monitor once

### pulse init

```text
Create the configuration folder with a starter setup.

Usage:
  pulse init [flags]

Flags:
  -config string
    	configuration directory to create (default "/etc/brynko-pulse")
  -json
    	print JSON
  -server-name string
    	name shown in alerts (default: this machine's host name)
```

### pulse validate

```text
Check the configuration and explain any problems.

Usage:
  pulse validate [flags]

Flags:
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -json
    	print JSON
```

### pulse discover

```text
Find services, containers and ports to monitor, and suggest monitors.

Usage:
  pulse discover [--apply] [flags]

Flags:
  -apply
    	save every suggestion as monitors/<id>.yaml
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -json
    	print JSON
```

### pulse suggest

```text
List what a monitor could watch here: filesystems, services, containers, ports.

Usage:
  pulse suggest [flags]

Lists what this server has, so a form can offer it instead of a text box:
  disk        filesystems that hold data, with their size and use
  service     running services
  container   running Docker containers
  port        TCP ports something listens on

Unlike pulse discover, it proposes no monitors and lists things already watched too.

Flags:
  -json
    	print JSON
```

### pulse add

```text
Add a monitor with one command.

Usage:
  pulse add <type> [target] [flags] [-- program args...]

Examples:
  pulse add disk /data --above 85
  pulse add memory
  pulse add service nginx --notify ops-email
  pulse add port 5432
  pulse add http https://example.com/health --status 200
  pulse add ssl example.com --days 21
  pulse add file /var/backups/db.tar.gz --when "age > 1d"
  pulse add heartbeat --id nightly-backup --every 1d
  pulse add command --id queue-ok -- /usr/local/bin/check-queue --max 100

Types: disk, memory, cpu, swap, load, service, process, container, port, http,
ssl, file, heartbeat, command.
The monitor is saved as monitors/<id>.yaml after the whole configuration is checked.

Flags:
  -above string
    	alert above this percent (disk, memory, cpu, swap) or load per CPU
  -clear-when string
    	when it counts as fixed again
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -days int
    	ssl: alert when fewer days are left
  -dry-run
    	show the monitor and check it, without saving
  -every string
    	heartbeat: how often the job pings, for example 1d
  -for string
    	how long it must stay wrong before alerting, for example 2m
  -grace string
    	heartbeat: extra time allowed before alerting
  -id string
    	monitor id (default: made from the type and target)
  -interval string
    	how often to check, for example 30s
  -json
    	print JSON
  -name string
    	name shown in alerts
  -notify string
    	channels, comma separated (default: the defaults in pulse.yaml)
  -severity string
    	info, warning or critical
  -status int
    	http: the status code to expect
  -when string
    	full alert condition, for example "age > 1d"
```

### pulse config

```text
Show or change the configuration, one file at a time.

Usage:
  pulse config show | get <file> | set <file> | delete <file> [flags]

  pulse config show            the whole configuration, as tools and forms read it
  pulse config get <file>      print one file, for example monitors/web.yaml
  pulse config set <file>      replace one file with what is read from standard input
  pulse config delete <file>   remove one monitor file

set and delete check the whole configuration first and change nothing if it
would be invalid. The running service picks up the change.

set --dry-run only checks: it reports what is wrong and, for a monitor file,
runs each monitor in it once and shows what it measures. Nothing is saved.

Flags:
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
  -dry-run
    	set: check the file and test its monitors, without saving
  -json
    	print JSON
```

### pulse secret

```text
Store or remove a secret used by channels.

Usage:
  pulse secret set <NAME> | secret delete <NAME> [flags]

  pulse secret set <NAME>      store the value read from standard input in secrets.env
  pulse secret delete <NAME>   remove it

The value is read from standard input so it never appears in the process list
or shell history. Refer to it in channels.yaml as ${NAME}.

Flags:
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -json
    	print JSON
```

### pulse test

```text
Send a test alert, or run one monitor once.

Usage:
  pulse test channel <name> | test monitor <id> [flags]

  pulse test channel <name>   sends a test alert through one channel
  pulse test monitor <id>     runs one monitor once and shows what it measures

Flags:
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
  -json
    	print JSON
```

## Run

- [`pulse install`](#pulse-install): Run pulse as a background service that starts at boot
- [`pulse uninstall`](#pulse-uninstall): Stop and remove the service (keeps the configuration)
- [`pulse run`](#pulse-run): Start monitoring in this terminal (runs until stopped)
- [`pulse status`](#pulse-status): Show every monitor and channel right now
- [`pulse history`](#pulse-history): Show recent alerts
- [`pulse silence`](#pulse-silence): Pause alerts for a while, for example during a deploy
- [`pulse fix`](#pulse-fix): Approve or dismiss a fix that self-healing prepared
- [`pulse ping`](#pulse-ping): Record a heartbeat, for jobs that check in

### pulse install

```text
Run pulse as a background service that starts at boot.

Usage:
  pulse install [flags]

Flags:
  -config string
    	configuration directory (created with a starter setup if missing) (default "/etc/brynko-pulse")
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
  -json
    	print JSON
  -no-discover
    	on a first install, do not add monitors for what discover finds
  -no-docker
    	do not let the service use Docker; container monitors will not work (Linux)
```

### pulse uninstall

```text
Stop and remove the service (keeps the configuration).

Usage:
  pulse uninstall [--purge] [flags]

Flags:
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
  -json
    	print JSON
  -purge
    	also delete the configuration, secrets and alert history
  -yes
    	with --purge: do not ask for confirmation
```

### pulse run

```text
Start monitoring in this terminal (runs until stopped).

Usage:
  pulse run [flags]

Flags:
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
```

### pulse status

```text
Show every monitor and channel right now.

Usage:
  pulse status [flags]

Flags:
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
  -json
    	print JSON
```

### pulse history

```text
Show recent alerts.

Usage:
  pulse history [flags]

Flags:
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
  -json
    	print JSON
  -limit int
    	how many alerts to show, newest first (default 20)
```

### pulse silence

```text
Pause alerts for a while, for example during a deploy.

Usage:
  pulse silence <monitor-id|all> <duration> [--reason text] | silence list | silence clear <monitor-id|all>

Examples:
  pulse silence web 1h --reason deploy   one monitor
  pulse silence all 30m                  every monitor
  pulse silence list                     silences in force
  pulse silence clear web                end one early ("all" ends every one)

Monitors keep being checked while silenced. If one is still failing when the
silence ends, you get one alert.

Flags:
  -config string
    	configuration directory (default "/etc/brynko-pulse")
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
  -json
    	print JSON
  -reason string
    	why, shown in pulse status and the history
```

### pulse fix

```text
Approve or dismiss a fix that self-healing prepared.

Usage:
  pulse fix approve <monitor-id> | fix dismiss <monitor-id> [--id fix-id] [flags]

Examples:
  pulse fix approve web     run the fix waiting for monitor web
  pulse fix dismiss web     turn it down; no other fix is offered until web works again

A fix waits for approval when its monitor's heal mode is ask, or when automatic
fixes did not help. pulse status shows what it would do. Use --id to approve only
the fix that status showed, not a newer one.

Flags:
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
  -id string
    	approve or dismiss only this fix (from pulse status --json)
  -json
    	print JSON
```

### pulse ping

```text
Record a heartbeat, for jobs that check in.

Usage:
  pulse ping <heartbeat-monitor-id> [flags]

Flags:
  -data string
    	where state and alert history are kept (default "/var/lib/brynko-pulse")
```

## Other

- [`pulse schema`](#pulse-schema): Print the JSON Schema for the configuration files
- [`pulse version`](#pulse-version): Show the version

### pulse schema

```text
Print the JSON Schema for the configuration files.

Usage:
  pulse schema
```

### pulse version

```text
Show the version.

Usage:
  pulse version [flags]

Flags:
  -json
    	print JSON
```

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | The command ran but failed, or the configuration is invalid |
| 2 | Wrong usage: unknown command, flag or arguments |
