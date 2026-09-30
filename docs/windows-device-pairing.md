# Windows device pairing

Parsar supports native pairing on Windows 10 and 11 x64. The target computer
does not need Bash, Git Bash, WSL, administrator privileges, a Windows
service, or a PATH change.

## Pair a device

In the Web admin UI, open **Pair a new device**, select **Windows
(PowerShell)**, and copy the generated command into Windows PowerShell. The
command downloads the Windows daemon from the configured Parsar server first,
then falls back to the matching GitHub Release asset, and starts
`parsar-daemon.exe connect -b`.

The pairing token is passed through short-lived environment variables. It is
not included in the download URL or daemon process arguments. The token is
single-use; close the dialog only after copying the command.

The installer endpoint is:

```text
GET /api/v1/parsar-daemon/install.ps1
```

The script accepts these optional environment variables:

- `PARSAR_DAEMON_VERSION` — release tag to download.
- `PARSAR_DAEMON_REPO` — GitHub `owner/name` override.
- `PARSAR_DAEMON_OUT_DIR` — directory for the downloaded executable (defaults
  to the user's Downloads directory).
- `PARSAR_DAEMON_CONNECT_URL` — Parsar server origin used for pairing and the
  self-hosted binary endpoint.
- `PARSAR_DAEMON_CONNECT_TOKEN` — one-time pairing token.
- `PARSAR_DAEMON_CONNECT_DEVICE_NAME` — device label.

## Manage the background daemon

The daemon keeps its profile state, PID file, and log below:

```text
%USERPROFILE%\.parsar\parsar-daemon\<profile>\
```

Use the same executable to inspect or stop the paired profile:

```powershell
parsar-daemon.exe status --profile default
parsar-daemon.exe logs --profile default
parsar-daemon.exe stop --profile default
```

`stop` requests a graceful shutdown through a user-scoped local control pipe
and falls back to bounded native process termination if the daemon is
unresponsive. The local control pipe is not exposed over the network.

Windows ARM64, Windows Service registration, MSI/MSIX packaging, automatic
login startup, and executable code signing are not part of the initial
Windows support target.
