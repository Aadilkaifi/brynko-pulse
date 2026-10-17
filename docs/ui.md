# Using pulse from an app

pulse is a command-line program with plain configuration files, and everything on this site works
without any other software. If you would rather click than edit YAML, the **brynko devOps Agent**
desktop app gives pulse a full interface. It is a separate program from brynko; download it from
[cloudagent.brynko.in/download](https://cloudagent.brynko.in/download).

The app is optional. pulse runs on the server by itself, keeps watching when the app is closed, and
sends its alerts directly. The app only reads and changes pulse's configuration, the same way you
would on the command line.

## What you need

- The server added in the app (**Servers**), reached over SSH with a key.
- **Linux:** pulse needs root for its configuration. Turn on **Allow sudo elevation** for the server
  in the app. If the server asks for a sudo password, the app asks you for it once and keeps it in
  memory only for that session.
- **Windows:** connect with an SSH user in the Administrators group (OpenSSH Server for Windows).

## Installing

Open **Monitoring**, pick the server and choose **Install pulse**. The app downloads the release
for that server, checks it against the release's `SHA256SUMS`, uploads it over your SSH connection,
checks it again on the server, and runs `pulse install`. Nothing is downloaded on the server itself
and nothing is piped into a shell. Disk, memory, CPU and swap are watched at once; the services,
containers and web ports it finds are listed, switched off, for you to turn on. Running **Install**
again on a server that has pulse upgrades it in place.

To open a server's monitoring from the server list, use **Open in Monitoring** on the server's page.

## The Monitoring page

For each server:

- **Monitors:** every monitor with what it alerts on in words ("used space of / is above 85% for 2
  minutes"), what it measures now and its state. The **Active** switch turns a monitor on or off
  (`enabled` in its file); inactive monitors are greyed at the bottom. Click a monitor to edit it.
- **Add monitor:** a form built from pulse itself: the monitor types and their values come from
  `pulse schema`, and the server's filesystems, services, containers and ports from `pulse suggest`.
  **Validate settings** checks the change with pulse and measures the monitor once without saving
  anything; **Save** writes it through `pulse config set`, which refuses anything invalid.
- **Channels:** add, edit and test email, WhatsApp, Telegram, Slack and webhook channels. Passwords
  and tokens go to `secrets.env` on the server and are never shown again.
- **Silences:** pause one monitor or all of them for a while, with a reason.
- **History:** the alerts pulse sent, newest first.
- **Settings:** the defaults every monitor follows (severity, channels, how long a problem must
  last, how often alerts repeat, how often monitors are checked) and weekly maintenance windows.
  Only those parts of `pulse.yaml` change; the rest of the file and its comments stay as they were.

The page also offers to name the server in alerts the way the app knows it (`server_name` and
`server_address` in `pulse.yaml`), so an email says "web-1 (203.0.113.10)" rather than a cloud host
name.

## Uninstalling

**Uninstall pulse**, in the server's menu (the three dots), asks what to do with pulse's data:

- **Keep its data:** the service and program are removed; monitors, channels, secrets and history
  stay on the server, and installing again picks them up (`pulse uninstall`).
- **Wipe everything:** the configuration, secrets and history are deleted too
  (`pulse uninstall --purge`). The app asks you to type the server's name first.

## The assistant

The app has an AI assistant that knows pulse. It reads the same things you would: what each server
watches, what is failing and since when, the alert history, and this guide, which ships inside the
app so it always matches the pulse it installs. Ask it in your own words:

- "Is anything wrong on web-1?" It checks pulse and the server and tells you what failed and why.
- "Watch the payments API and tell me if its health check stops saying ok." It reads which monitor
  types the server offers, writes the website monitor with the right JSON check, and shows it to you.
- "Silence the database alerts for the next hour, we are migrating." It sets the silence with your
  reason.
- "Why did I get three disk alerts last night?" It reads the history and the monitor's rule.

It can silence monitors, turn them on or off, add or change monitors, and install or upgrade pulse,
but every change waits for you: the app shows a card with exactly what will change, and nothing
happens until you approve it. Reading is free; changing always needs your yes. Removing pulse is
not something the assistant does; that stays with **Uninstall** in the server's menu.

## How the app talks to pulse

Everything goes through pulse's own commands, over SSH, with `--json`: `status`, `config show`,
`config get`, `config set --dry-run`, `config set`, `secret set`, `silence`, `history`, `schema`,
`suggest`, `install` and `uninstall`. Any tool can do the same; see
[Configuration](configuration.md#changing-it-from-scripts-and-tools) and the
[command reference](cli.md).
