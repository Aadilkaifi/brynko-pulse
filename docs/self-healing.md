# Self-healing

pulse can fix problems for you. Give a monitor a `heal` rule with the steps that fix it: restart
services, restart containers, or run a program. When the monitor fails, pulse prepares that fix
and either waits for you to approve it, or runs it by itself, and tells you what happened:

> **[Failed] web-1: brynko.in: not answering (status 502)**
> A fix is waiting for your approval: restart php8.3-fpm and nginx. Approve it with sudo pulse fix
> approve site, or from brynko devOps Agent.

> **[Fixed] web-1: brynko.in was fixed by pulse after 2m**
> pulse restarted php8.3-fpm and nginx at 10:03 after your approval and it works again.

Self-healing is off unless you add a `heal` rule to a monitor. Monitors that `pulse install` finds
never get one by themselves.

## Turn it on

```yaml
monitors:
  - id: site
    type: http
    url: https://brynko.in
    heal:
      steps:
        - restart_service: [php8.3-fpm, nginx]
      only_if_ok: [mysql]
```

That fix waits for your approval (mode `ask`, the default). The other settings have defaults:

| Setting | Default | What it does |
|---|---|---|
| `mode` | `ask` | `ask`: prepare the fix and wait for approval. `auto`: run it by itself. `off`: keep the rule, do nothing |
| `steps` | (required) | What to do, in order; see [Steps](#steps) |
| `only_if_ok` | | Monitors that must be OK before a fix; see [Dependencies](#dependencies) |
| `attempts` | `3` | How many automatic fixes within `within` |
| `within` | `1h` | The window automatic fixes are counted in |
| `verify` | `2m` | How long pulse waits after a fix for the monitor to work again |
| `timeout` | `90s` | How long all the steps together may take |

Then check and apply it as usual: `sudo pulse validate`, then `sudo pulse install` once, which gives
pulse the right to restart those services (see [What pulse is allowed to do](#what-pulse-is-allowed-to-do)).
After that, `sudo pulse config set` keeps the right in step with your heal rules.

A fix helps when the problem comes from something that can be restarted or repaired by a program.
`ssl`, `file` and `heartbeat` monitors cannot have one: a restart does not renew a certificate,
refresh a file or make a job check in.

## Steps

Each step does one thing. pulse runs them in order and stops at the first that fails.

| Step | What it does |
|---|---|
| `restart_service: [a, b]` | Restarts these services in order: systemd units on Linux (without `.service`), Windows services on Windows |
| `restart_container: [api]` | Restarts these Docker containers in order |
| `run: [program, arg, ...]` | Runs your own program, for fixes a restart cannot do |

`run` is a list (program first, then arguments) and is never passed through a shell. To run a
script, start its interpreter: on Linux `[/bin/sh, /opt/fix/clear-cache.sh]`; on Windows
`[powershell.exe, -NoProfile, -ExecutionPolicy, Bypass, -File, 'C:\fix\clear-cache.ps1']`. The step
counts as done when the program exits with 0; anything else counts as a failed fix, and its last
output is shown in the alert. The program runs like a `command` check:

- On Linux as the `brynko-pulse` account, inside the service's sandbox: it can use the network and
  Docker (when pulse may use Docker), but the system is read-only to it except pulse's data folder.
  A fix that must write elsewhere should call an API or a tool that has that right.
- On Windows as the pulse service account.

## Ask or auto

**Ask** (the default) suits most fixes: you decide, with the alert in front of you.

1. The monitor fails, as usual (its `alert_when`, for its `for`).
2. Once the problem has lasted the monitor's `for`, and at least 30 seconds, pulse prepares the fix
   and sends the alert, which says what the fix would do.
3. You approve it with `sudo pulse fix approve <monitor>` (or from brynko devOps Agent), or turn it
   down with `pulse fix dismiss <monitor>`. A dismissed problem gets no other offer until it is over.
4. pulse runs the steps, waits up to `verify`, and tells you whether it worked. When it did not, it
   says so and prepares the fix again.

A prepared fix is dropped when the monitor works again by itself, and prepared afresh after an hour,
so an approval always answers the problem as it is now. `pulse status --json` shows the fix that is
waiting, with an `id`; `pulse fix approve <monitor> --id <id>` approves only that one.

**Auto** suits fixes you trust, such as restarting a worker that sometimes hangs.

1. Once the problem has lasted long enough, pulse runs the steps and waits up to `verify`.
2. **It works:** you get one message, "Fixed", saying what failed and what pulse did. If it was fixed
   before the first alert went out, that is the only message you get about it.
3. **It still fails:** you get the alert, saying what pulse tried. pulse waits a minute, then tries
   once more.
4. **Two automatic fixes did not help**, or the `attempts` within `within` are used up: the next fix
   waits for your approval, as in ask mode, and the alert says why. Restarting the same thing again
   and again rarely helps when the cause is elsewhere.

## Dependencies

A website that fails because its database is down is not fixed by restarting the website. List the
monitors it depends on in `only_if_ok`: while one of them is failing, pulse does not fix or offer a
fix for this monitor, and the alert names the likely cause:

> pulse did not try to fix it: mysql is failing too, which is the likely cause.

When the dependency works again and this monitor still fails, pulse goes on as usual.

## When pulse does not act by itself

- **During a maintenance window.** People are working on the server; pulse keeps its hands off.
- **While the monitor is flapping** (switching between failing and working).
- **While a monitor in `only_if_ok` is failing.**
- **When the fix cannot work**, for example when pulse lacks the right to restart a service. It says
  so once, with the reason, instead of trying again.
- **Beyond the server's limit:** `limits.max_heals_per_hour` in `pulse.yaml` (default 10) caps
  automatic fixes across all monitors; beyond it, fixes wait for approval.

A fix you approve runs even then: you decided. A **silence** only mutes alerts: pulse still fixes
what it can, and records it in the history.

## What pulse is allowed to do

pulse runs with no extra rights, and that does not change. For `restart_service` steps,
`pulse install` (and `pulse config set` when run as root or Administrator) grants one narrow right:
the pulse service may restart exactly the services named in heal steps, and nothing else. It cannot
stop them for good, change them, or touch any other service. When you remove the step, the right
goes; `pulse uninstall` removes it too.

- **Linux:** a polkit rule, `/etc/polkit-1/rules.d/60-brynko-pulse.rules`, that lets the
  `brynko-pulse` account restart the listed units through systemd. It needs polkit 0.106 or newer
  (Ubuntu 24.04, Debian 12, RHEL 9 and later). On older systems, such as Ubuntu 22.04, polkit cannot
  limit a right to single services, so `pulse install` warns and `restart_service` does not work
  there; `run` and `restart_container` still do.
- **Windows:** start and stop rights on each listed service for `NT SERVICE\brynko-pulse`.
- **Containers** need no extra right: pulse already uses Docker when it may (see
  [Install](install.md#what-install-sets-up)).

Approving a fix needs root (or Administrator): `pulse fix` hands the approval to the running
service through its data folder. pulse never listens on a port, and an approval only names the
monitor; what the fix does always comes from your configuration.

## Seeing what it did

- `pulse status --json` gives each monitor with a heal rule a `heal` object: `mode`, `what` (the fix
  in words), `attempts`, `attempts_used` (automatic fixes within the window), `healing` (a fix is
  running or settling), `gave_up`, `waiting` (a fix waiting for approval: `id`, `since`, `expires`,
  `why`), `approved`, `dismissed`, `last_at` and `last_result`.
- `pulse history` lists every fix, approval and dismissal as kind `heal`, beside the alerts.
- Alerts have kind `healed` ("Fixed") or `heal_failed` ("Could not fix", also when automatic fixes
  did not help and the next one waits for you), and a `heal` field that says what pulse did or what
  waits. Webhooks receive the same fields.
