# Install

pulse is one program, `pulse`, with no runtime to install. It runs on Linux (amd64, arm64) and
Windows (amd64). Every release on the
[releases page](https://github.com/Aadilkaifi/brynko-pulse/releases) has the binaries, `.deb` and
`.rpm` packages, the install script and a `SHA256SUMS` file to check them against.

After installing, pulse runs as a background service that starts at boot. A first install also
sets up monitors for what already runs on the server (`--no-discover` skips this). Disk, memory, CPU
and swap start active. Every running service and Docker container, and the web server's ports, are
added inactive (`enabled: false`): turn on the ones that matter to you. Add a channel so alerts
reach you: see [Channels](channels.md).

You can also install and manage pulse from the brynko devOps Agent desktop app, over SSH: see
[Using pulse from an app](ui.md).

## Linux: one command

```sh
curl -fsSL https://github.com/Aadilkaifi/brynko-pulse/releases/latest/download/install.sh | sudo sh
```

The script:

1. finds your CPU type and package manager,
2. downloads the matching package from the latest release and its `SHA256SUMS`,
3. stops if the checksum does not match,
4. installs the `.deb` (apt), the `.rpm` (dnf or yum), or else the plain binary,
5. runs `pulse install`, which creates the configuration and starts the service.

To read the script before running it, download it first:

```sh
curl -fsSLO https://github.com/Aadilkaifi/brynko-pulse/releases/latest/download/install.sh
less install.sh
sudo sh install.sh
```

Set `PULSE_VERSION=v1.2.3` to install a specific release instead of the latest.

## Linux: package

Download the package for your CPU (`amd64` for most servers, `arm64` for ARM such as AWS Graviton;
`uname -m` tells you) and install it:

```sh
sudo apt install ./brynko-pulse_<version>_linux_amd64.deb    # Debian, Ubuntu
sudo dnf install ./brynko-pulse_<version>_linux_amd64.rpm     # Fedora, RHEL, Rocky, Alma
```

The package puts `pulse` in `/usr/bin` and runs `pulse install` for you. Removing the package
(`sudo apt remove brynko-pulse`) stops and removes the service and keeps your configuration.

## Linux: plain binary

```sh
curl -fsSLO https://github.com/Aadilkaifi/brynko-pulse/releases/download/<version>/pulse_<version>_linux_amd64
curl -fsSLO https://github.com/Aadilkaifi/brynko-pulse/releases/download/<version>/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
chmod +x pulse_<version>_linux_amd64
sudo ./pulse_<version>_linux_amd64 install
```

`pulse install` copies itself to `/usr/local/bin/pulse`, so you can delete the downloaded file
afterwards.

## Windows

1. Download `pulse_<version>_windows_amd64.exe` and `SHA256SUMS` from the releases page.
2. Check the file in PowerShell; the two values must match:
   ```powershell
   (Get-FileHash .\pulse_<version>_windows_amd64.exe -Algorithm SHA256).Hash
   Select-String windows_amd64.exe .\SHA256SUMS
   ```
3. Open PowerShell with **Run as administrator** and run:
   ```powershell
   .\pulse_<version>_windows_amd64.exe install
   ```

`pulse install` copies itself to `C:\Program Files\brynko-pulse\pulse.exe` and registers the
`brynko-pulse` Windows Service. The binary is not code-signed yet, so Windows SmartScreen may warn
when you run a file downloaded with a browser; the checksum step above is how you know the file is
genuine.

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
- Journal log monitors need to read the systemd journal. Install adds the service user to the
  `systemd-journal` group when such a monitor exists, and removes it when none is left.
- Log files that the service user cannot read are listed as warnings by install.

## Upgrade

Run the install script again, install the newer package, or run the newer binary's
`pulse install`. pulse never updates itself. An upgrade keeps your configuration as it is; the
monitors for what runs on the server are only added on a first install (`pulse discover` lists
anything new since).

## Uninstall

```sh
sudo pulse uninstall           # stops and removes the service and program, keeps config and history
sudo pulse uninstall --purge   # also deletes config, secrets and history (asks you to type "delete")
```

With a package, remove the package instead (`sudo apt remove brynko-pulse` or
`sudo dnf remove brynko-pulse`).

On Windows, in PowerShell opened with **Run as administrator**:

```powershell
& "C:\Program Files\brynko-pulse\pulse.exe" uninstall            # keeps config and history
& "C:\Program Files\brynko-pulse\pulse.exe" uninstall --purge    # also deletes them
```

Windows does not delete a program while it runs, so `pulse.exe` itself is removed at the next
restart; the service is gone at once.

## From source

See [Building from source](build.md).
