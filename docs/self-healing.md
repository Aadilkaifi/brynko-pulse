# Self-healing

pulse can fix some problems by itself. When a monitor with a `heal` rule fails, pulse tries the fix
you gave it, a limited number of times, and tells you what it did:

> **[Fixed] web-1: nginx was fixed by pulse after 1m**
> It failed (the service is stopped). pulse restarted nginx at 10:03 and it works again.

> **[Could not fix] web-1: nginx: the service is stopped**
> pulse tried to restart nginx 3 times in 1h and it still fails. It stops trying until this works again.

Self-healing is off unless you add a `heal` rule to a monitor. Monitors that `pulse install` finds
never get one by themselves.

## Turn it on

Add `heal` to the monitor:

```yaml
monitors:
  - id: service-nginx
    type: service
    service: nginx
    heal:
      action: restart
```

That is all you need. The other settings have defaults:

| Setting | Default | What it does |
|---|---|---|
| `action` | (required) | `restart` or `run`, see below |
| `run` | | For `run`: the program and its arguments, as a list |
| `attempts` | `3` | How many tries within `within` before pulse gives up |
| `within` | `1h` | The window the attempts are counted in |
| `verify` | `2m` | How long pulse waits after a fix for the monitor to work again |
| `timeout` | `90s` | How long one fix may take |

Then check and apply it as usual: `sudo pulse validate`, then `sudo pulse install` once, which gives
pulse the right to restart that service (see [What pulse is allowed to do](#what-pulse-is-allowed-to-do)).
After that, `sudo pulse config set` keeps the right in step with your heal rules.

## Fixes

### `restart`

Restarts what the monitor watches. Works for `service` monitors (a systemd unit on Linux, a Windows
Service on Windows) and `container` monitors (Docker, Linux).

### `run`

Runs your own program, for fixes a restart cannot do: drain a queue, clear an application cache
through its API, recycle a worker pool.

```yaml
  - id: queue-depth
    type: command
    run: ["/usr/local/bin/queue-depth"]
    parse: number
    alert_when: value > 1000
    heal:
      action: run
      run: ["/usr/local/bin/drain-queue", "--all"]
      timeout: 5m
      verify: 5m
```

`run` is a list (program first, then arguments) and is never passed through a shell. The fix counts
as done when the program exits with 0; anything else counts as a failed try, and its last output is
shown in the alert. The program runs like a `command` check:

- On Linux as the `brynko-pulse` account, inside the service's sandbox: it can use the network and
  Docker (when pulse may use Docker), but the system is read-only to it except pulse's data folder.
  A fix that must write elsewhere should call an API or a tool that has that right.
- On Windows as the pulse service account.

## How it decides

1. The monitor fails, as usual (its `alert_when`, for its `for`).
2. pulse waits until the problem has lasted the monitor's `for`, and at least 30 seconds, so a single
   bad reading never triggers a fix.
3. It does the fix, waits up to `verify`, and checks again.
4. **It works:** you get one message, "Fixed", saying what failed and what pulse did. If it was fixed
   before the first alert went out, that is the only message you get about it.
5. **It still fails:** you get the alert, saying what pulse tried, and pulse tries again.
6. **No tries left** (`attempts` within `within`): you get one critical "Could not fix" message and
   pulse stops trying until the monitor works again. The usual repeat alerts continue.

pulse does not try a fix:

- **During a maintenance window.** People are working on the server; pulse keeps its hands off.
- **While the monitor is flapping** (switching between failing and working).
- **When the fix cannot work**, for example when pulse lacks the right to restart the service. It
  says so once, with the reason, instead of trying again.
- **Beyond the server's limit:** `limits.max_heals_per_hour` in `pulse.yaml` (default 10) caps fixes
  across all monitors, so a bad change cannot set off a storm of restarts.

A **silence** only mutes alerts: pulse still fixes what it can, and records it in the history.

## What pulse is allowed to do

pulse runs with no extra rights, and that does not change. For `heal: restart` on a service,
`pulse install` (and `pulse config set` when run as root or Administrator) grants one narrow right:
the pulse service may restart exactly the services that have `heal: restart`, and nothing else. It
cannot stop them for good, change them, or touch any other service. When you remove the heal rule,
the right goes; `pulse uninstall` removes it too.

- **Linux:** a polkit rule, `/etc/polkit-1/rules.d/60-brynko-pulse.rules`, that lets the
  `brynko-pulse` account restart the listed units through systemd. It needs polkit 0.106 or newer
  (Ubuntu 24.04, Debian 12, RHEL 9 and later). On older systems, such as Ubuntu 22.04, polkit cannot
  limit a right to single services, so `pulse install` warns and `heal: restart` on services does not
  work there; `heal: run` and container restarts still do.
- **Windows:** start and stop rights on each listed service for `NT SERVICE\brynko-pulse`.
- **Containers** need no extra right: pulse already uses Docker when it may (see
  [Install](install.md#what-install-sets-up)).

## Seeing what it did

- `pulse status --json` gives each monitor with a heal rule a `heal` object: `action`, `attempts`,
  `attempts_used` (within the window), `healing` (a fix is running or settling), `gave_up`,
  `last_at` and `last_result`.
- `pulse history` lists every fix as kind `heal`, beside the alerts.
- Alerts have kind `healed` ("Fixed") or `heal_failed` ("Could not fix"), and a `heal` field that
  says what pulse did. Webhooks receive the same fields.
