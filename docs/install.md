# Install

Copy one command, run it, and pulse is watching the server. It starts again after every reboot.

## Linux

Run this on the server (Ubuntu, Debian, RHEL, Rocky, Alma, Fedora and others; Intel/AMD or ARM):

```sh
curl -fsSL https://github.com/Aadilkaifi/brynko-pulse/releases/latest/download/install.sh | sudo sh
```

Check it is running:

```sh
sudo pulse status
```

## Windows

Open **PowerShell** with **Run as administrator** (right-click PowerShell, *Run as administrator*),
paste this whole block and press Enter:

```powershell
& {
  $ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue'
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  $v    = (Invoke-RestMethod https://api.github.com/repos/Aadilkaifi/brynko-pulse/releases/latest).tag_name
  $base = "https://github.com/Aadilkaifi/brynko-pulse/releases/download/$v"
  $exe  = "$env:TEMP\pulse_${v}_windows_amd64.exe"; $sums = "$env:TEMP\pulse_SHA256SUMS"
  Invoke-WebRequest "$base/pulse_${v}_windows_amd64.exe" -OutFile $exe -UseBasicParsing
  Invoke-WebRequest "$base/SHA256SUMS" -OutFile $sums -UseBasicParsing
  $want = ((Select-String -Path $sums -Pattern "pulse_${v}_windows_amd64.exe").Line -split '\s+')[0]
  if ((Get-FileHash $exe -Algorithm SHA256).Hash -ne $want) { throw "Checksum mismatch. Nothing was installed." }
  & $exe install
  Remove-Item $exe, $sums
}
```

Check it is running:

```powershell
& "C:\Program Files\brynko-pulse\pulse.exe" status
```

Both commands download the latest release, check it against the release's `SHA256SUMS`, stop if it
does not match, and install pulse as a service.

## Next: get the alerts

pulse is now watching disk, memory, CPU and swap, but it has nowhere to send alerts yet. Add a
channel (email, WhatsApp, Telegram, Slack or webhook) as shown in [Channels](channels.md), then
send yourself a test alert.

It has also listed the services, Docker containers and web ports it found, switched off. Turn on
the ones that matter to you: see [Monitors](monitors.md) and [Configuration](configuration.md).

Prefer clicking to editing files? The brynko devOps Agent desktop app installs and manages pulse
over SSH: see [Using pulse from an app](ui.md).

## Upgrade

Run the same install command again. It replaces the program and keeps your monitors, channels,
secrets and history. pulse never updates itself.

New services or containers since the first install are not added on their own; `sudo pulse
discover` lists them.

## Uninstall

Linux:

```sh
sudo pulse uninstall           # removes the service and program, keeps config and history
sudo pulse uninstall --purge   # also deletes config, secrets and history (asks you to type "delete")
```

Installed from a package? Remove the package instead: `sudo apt remove brynko-pulse` or
`sudo dnf remove brynko-pulse`.

Windows, in PowerShell with **Run as administrator**:

```powershell
& "C:\Program Files\brynko-pulse\pulse.exe" uninstall            # keeps config and history
& "C:\Program Files\brynko-pulse\pulse.exe" uninstall --purge    # also deletes them
```

The service is gone at once. Windows does not delete a running program, so `pulse.exe` itself is
removed at the next restart.

## Other ways to install

Everything below is optional. Each release on the
[releases page](https://github.com/Aadilkaifi/brynko-pulse/releases) has the programs, `.deb` and
`.rpm` packages, the install script and `SHA256SUMS`.

### Read the Linux script before running it

```sh
curl -fsSLO https://github.com/Aadilkaifi/brynko-pulse/releases/latest/download/install.sh
less install.sh
sudo sh install.sh
```

The script finds your CPU type and package manager, downloads the matching package and
`SHA256SUMS`, stops if the checksum does not match, installs the `.deb` (apt), the `.rpm` (dnf or
yum) or else the plain program, and runs `pulse install`.

To install a specific release instead of the latest, set `PULSE_VERSION`:

```sh
curl -fsSL https://github.com/Aadilkaifi/brynko-pulse/releases/latest/download/install.sh | sudo PULSE_VERSION=v0.1.1 sh
```

### Linux package

Download the package for your CPU from the releases page (`amd64` for most servers, `arm64` for ARM
such as AWS Graviton; `uname -m` tells you) and install it:

```sh
sudo apt install ./brynko-pulse_v0.1.1_linux_amd64.deb    # Debian, Ubuntu
sudo dnf install ./brynko-pulse_v0.1.1_linux_amd64.rpm     # Fedora, RHEL, Rocky, Alma
```

The package puts `pulse` in `/usr/bin` and runs `pulse install` for you.

### Linux program only

```sh
curl -fsSLO https://github.com/Aadilkaifi/brynko-pulse/releases/download/v0.1.1/pulse_v0.1.1_linux_amd64
curl -fsSLO https://github.com/Aadilkaifi/brynko-pulse/releases/download/v0.1.1/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
chmod +x pulse_v0.1.1_linux_amd64
sudo ./pulse_v0.1.1_linux_amd64 install
```

`pulse install` copies itself to `/usr/local/bin/pulse`, so you can delete the downloaded file
afterwards.

### Windows by hand

1. Download `pulse_v0.1.1_windows_amd64.exe` and `SHA256SUMS` from the releases page.
2. In PowerShell, check the file; the two values must match:
   ```powershell
   (Get-FileHash .\pulse_v0.1.1_windows_amd64.exe -Algorithm SHA256).Hash
   Select-String windows_amd64.exe .\SHA256SUMS
   ```
3. In PowerShell with **Run as administrator**:
   ```powershell
   .\pulse_v0.1.1_windows_amd64.exe install
   ```

The program is not code-signed yet, so Windows SmartScreen may warn about a file downloaded with a
browser; the checksum is how you know the file is genuine.

### Install options

`pulse install` takes two options:

- `--no-discover`: do not add monitors for the services, containers and ports found on the server
  (they are added switched off anyway). Works on a first install with the program-only and Windows
  methods, for example `sudo ./pulse_v0.1.1_linux_amd64 install --no-discover`.
- `--no-docker`: do not give the service access to Docker (see below). After any install, run
  `sudo pulse install --no-docker` to take that access away again.

## What install sets up

| | Linux | Windows |
|---|---|---|
| Program | `/usr/local/bin/pulse` (or `/usr/bin/pulse` from a package) | `C:\Program Files\brynko-pulse\pulse.exe` |
| Configuration | `/etc/brynko-pulse` | `%ProgramData%\brynko-pulse` |
| State and history | `/var/lib/brynko-pulse` | `%ProgramData%\brynko-pulse\data` |
| Service | systemd unit `brynko-pulse` | Windows Service `brynko-pulse` |
| Runs as | system user `brynko-pulse`, no extra rights | virtual account `NT SERVICE\brynko-pulse` |
| Logs | `journalctl -u brynko-pulse` | Event Viewer, Windows Logs, Application |

The service can read the configuration and write only its data folder. Running `pulse install`
again is safe: it updates the program and the service in place.

**Access the service gets:**

- **Docker:** where Docker is installed, the service user joins the `docker` group so container
  monitors work. pulse only reads container state and events, but membership of that group is
  equivalent to root. If you do not want that, install with `--no-docker`; running install again
  with or without the flag adds or removes the access.
- **Journal:** log monitors that read the systemd journal need the `systemd-journal` group. Install
  adds the service user to it when such a monitor exists, and removes it when none is left.
- Log files that the service user cannot read are listed as warnings by install.

## From source

See [Building from source](build.md).
