# Windows Device Pairing Support

## Document status

- Status: implementation design for review
- Target branch: `feat/windows-device-support`
- Initial platform target: Windows 10/11 x64 (`windows/amd64`)
- Out of scope for the first delivery: Windows ARM64, Windows Service registration,
  MSI/MSIX/package-manager installers, automatic start at login, and executable
  code signing

This document is intentionally implementation-oriented. After the design is
approved, the work can be executed from the checklist without changing the
pairing protocol or database model.

## 1. Problem and desired outcome

The user-paired device flow works on macOS and Linux. It does not currently
work natively on Windows because:

1. The Web UI emits only a Bash command.
2. The installer is a Bash script and recognizes only `darwin` and `linux`.
3. Release artifacts and the server image contain no Windows daemon executable.
4. The daemon's background process implementation uses POSIX-only `setsid` and
   `syscall.Kill`.

The desired result is a Windows user experience equivalent to the existing
Unix flow:

1. An administrator clicks **Pair a new device**.
2. The Web UI shows a PowerShell command for Windows.
3. The user pastes the command into PowerShell; no Bash, Git Bash, or WSL is
   required.
4. The command downloads `parsar-daemon.exe`, consumes the one-time pairing
   token, and starts the daemon in the background.
5. The device becomes Online and can execute the selected Agent CLI.
6. `connect`, `status`, `logs`, and `stop` continue to work on every supported
   operating system.

The server-side pairing API, runner credential exchange, WebSocket protocol,
heartbeat, runtime records, and agent-daemon protocol remain unchanged.

## 2. Existing architecture and current flow

The relevant ownership boundary is already documented in `CONTRIBUTING.md`:

- The server owns pairing records, credentials, runtime bindings, and heartbeat
  state.
- `parsar-daemon` owns local CLI discovery, process spawning, cancellation,
  working-directory selection, and local state.
- The Web UI only creates the pairing request and presents a command.

The current flow is:

```text
PairDaemonDialog
  POST /api/v1/workspaces/{workspaceID}/runtimes
       └─ runtime + one-time pairing token
  Bash command: curl install.sh | env vars bash
       └─ install_parsar_daemon.sh
            ├─ GET /api/v1/parsar-daemon/download?os=...&arch=...
            ├─ fallback to GitHub Release
            └─ parsar-daemon connect -b
                 ├─ POST /api/v1/runtimes/pair
                 ├─ save credential below ~/.parsar/
                 └─ reverse WebSocket + heartbeat
```

There are two binary distribution paths and both must be updated:

1. **Self-host/server path:** the server image contains prebuilt daemon
   binaries and serves them from `/api/v1/parsar-daemon/download`.
2. **GitHub Release path:** the installer falls back to a public release when
   the server does not contain the requested binary.

“Server download” means that the target Windows computer downloads a daemon
   binary from the Parsar server. The server is not downloading anything from
   Windows.

## 3. Platform terminology and support policy

`GOOS/GOARCH` identifies an operating-system/CPU pair:

| Target | Meaning | Initial delivery |
|---|---|---:|
| `windows/amd64` | Intel/AMD 64-bit Windows (x64) | Yes |
| `windows/arm64` | ARM-based 64-bit Windows | No, follow-up |
| `darwin/amd64` | Intel macOS | Preserve |
| `darwin/arm64` | Apple Silicon macOS | Preserve |
| `linux/amd64` | Intel/AMD 64-bit Linux | Preserve |
| `linux/arm64` | ARM64 Linux | Preserve |

“Windows support” in this issue means native Windows x64 support. Adding ARM64
later is a release-matrix and compatibility decision, not a separate pairing
protocol.

## 4. Target design

### 4.1 Keep common behavior, isolate OS-specific mechanics

Do not replace the macOS/Linux implementation with a Windows implementation.
Create a small platform boundary with the same operations on every OS:

```text
StartDetached(executable, argv, env, stdin, stdout, stderr) -> pid
IsAlive(pid) -> error
RequestStop(pid) -> error
ForceKill(pid) -> error
```

The existing Unix behavior remains the reference implementation:

- `setsid` detaches the background child from the terminal.
- `SIGTERM` requests a graceful stop.
- `SIGKILL` is the bounded-time fallback.

The Windows implementation uses native process APIs and must expose the same
CLI behavior:

- Create a detached child with no inherited console dependency.
- Redirect stdin to the null device and stdout/stderr to the profile log.
- Write the child PID atomically to the same profile PID file.
- Detect whether the PID is still running.
- Stop the child through a user-scoped local control mechanism where possible,
  then force-terminate after the existing bounded timeout.

The preferred Windows graceful-stop design is a per-profile named pipe owned by
the daemon. `parsar-daemon stop` sends a `stop` message through that pipe; the
daemon cancels its root context and shuts down its router normally. If the pipe
is unavailable because the process is already unhealthy, the command falls
back to native process termination. The pipe must use a user-only security
descriptor and a name derived from the validated profile, not raw user input.
This preserves the graceful-stop contract without pretending that Unix signals
exist on Windows.

The named pipe is local lifecycle plumbing only. It is not a server protocol
and must not be exposed over the network.

### 4.2 Agent subprocesses and process trees

The daemon starts Agent CLI children through the shared process runner. The
runner must remain the single owner of start, stdin, cancellation, timeout, and
wait/reap behavior.

For Windows:

- Resolve `.exe`, `.cmd`, and PowerShell shim commands through `exec.LookPath`.
- Preserve absolute working-directory validation and `%USERPROFILE%\\.parsar`
  state placement.
- Close stdin before cancellation where appropriate.
- Use a Windows Job Object or an equivalent process-tree mechanism so an Agent
  CLI child cannot survive daemon shutdown as an orphan.
- Keep the existing one-hour idle lifetime for resumable conversations; explicit
  cancellation and daemon shutdown still terminate the process tree.

The daemon must advertise only the Agent kinds that pass the existing preflight
check. Windows support does not imply that an upstream Agent CLI is available
for every Windows installation.

### 4.3 PowerShell installation path

Add a PowerShell equivalent of the current Bash installer. It must:

1. Accept `PARSAR_DAEMON_VERSION`, `PARSAR_DAEMON_REPO`,
   `PARSAR_DAEMON_OUT_DIR`, `PARSAR_DAEMON_CONNECT_URL`,
   `PARSAR_DAEMON_CONNECT_TOKEN`, and
   `PARSAR_DAEMON_CONNECT_DEVICE_NAME`.
2. Detect `windows/amd64` for the initial release.
3. Try the configured Parsar server's binary endpoint first.
4. Fall back to the matching GitHub Release asset.
5. Save the file with a `.exe` extension.
6. Invoke `parsar-daemon.exe connect -b` without putting the token in command
   arguments or URL query parameters.
7. Avoid administrator privileges, registry writes, PATH mutation, and writes
   outside the configured output/profile directories.
8. Return a non-zero exit code with an actionable message for download, policy,
   pairing, or startup failures.

The PowerShell script should be served from a separate endpoint such as
`/api/v1/parsar-daemon/install.ps1`. Keep the source copy and the embedded
server copy synchronized just as the existing `.sh` installer is synchronized.

### 4.4 Web pairing command

The current `PairDaemonDialog` has one command builder for Bash. Replace the
single command block with a platform choice (tabs or two labeled blocks):

- macOS / Linux: existing Bash command, unchanged in meaning.
- Windows: PowerShell command that sets the connection environment variables
  and evaluates the server-provided `.ps1` installer.

The browser cannot reliably know which computer will receive the command, so
do not silently choose a platform from the browser's own OS. The user must be
able to select or copy the intended command.

PowerShell-specific quoting must escape the server URL, token, and device name
as data. The token remains a one-time value and must not be placed in a URL
query string or daemon process arguments.

### 4.5 Binary publication and self-hosted serving

Add `windows/amd64` to the daemon release workflow. Windows artifacts should
be named consistently, for example:

```text
parsar-daemon-vX.Y.Z-windows-amd64.exe
parsar-daemon-vX.Y.Z-windows-amd64.exe.sha256
```

Add the same target to the server image's cross-compilation loop and store the
server-served file as:

```text
parsar-daemon-windows-amd64.exe
```

Extend the download handler's allowlist and generated API description from
`darwin, linux` to `darwin, linux, windows`. The allowlist remains mandatory to
prevent path traversal through the `os` and `arch` query parameters.

No Windows container is required. The Linux build container cross-compiles the
Windows executable and stores it as a public artifact in the Parsar server
image.

## 5. File-level implementation checklist

### Daemon runtime

- [ ] Split `apps/parsar-daemon/internal/daemonize/fork.go` into shared logic
      plus Unix and Windows process-start implementations.
- [ ] Move `IsAlive` and stop/kill operations out of the POSIX-only portion of
      `apps/parsar-daemon/internal/daemonize/pidfile.go`.
- [ ] Add Windows local graceful-stop plumbing (named pipe) and a native force
      termination fallback.
- [ ] Make `connect.go` and `logs.go` use a cross-platform signal/shutdown
      helper instead of directly depending on Unix signal semantics.
- [ ] Remove direct signal assumptions from the shared Agent process runner;
      add Windows process-tree/job handling where required.
- [ ] Keep all profile state below `~/.parsar/` (on Windows this resolves below
      the user's profile directory).

### Installer and API

- [ ] Add `scripts/install-parsar-daemon.ps1`.
- [ ] Add the embedded server copy and `/api/v1/parsar-daemon/install.ps1`.
- [ ] Add tests for script rendering, repository override, platform selection,
      and token environment handling.
- [ ] Extend `server/internal/api/parsar_daemon_download.go` to accept Windows
      x64 and serve `.exe` artifacts safely.
- [ ] Update annotations and run `make openapi`; commit the generated OpenAPI
      file.

### Build and release

- [ ] Add `windows/amd64` to `.github/workflows/parsar-daemon-release.yml`.
- [ ] Use the `.exe` suffix for Windows release artifacts and checksums.
- [ ] Add the Windows target to the daemon cross-compilation loop in
      `Dockerfile`.
- [ ] Confirm server-image copy permissions do not try to `chmod` the Windows
      file as an executable inside Linux.
- [ ] Update release notes and installer fallback URLs.

### Web UI

- [ ] Add Bash/PowerShell platform selection to `PairDaemonDialog`.
- [ ] Add localized labels, descriptions, and copy-button states.
- [ ] Add UI tests for both command forms, quoting, and server public URL use.
- [ ] Keep the dialog within existing overflow and typography rules.

### Verification and documentation

- [ ] Add `windows-latest` CI coverage for daemon build and unit tests.
- [ ] Add Windows-specific tests for background start, stop, stale PID state,
      log output, and profile isolation.
- [ ] Run an end-to-end Windows x64 pairing test against a self-hosted server
      binary endpoint.
- [ ] Run an end-to-end GitHub Release fallback test.
- [ ] Validate at least one supported Agent CLI end to end, including
      cancellation and daemon shutdown.
- [ ] Run `make check` on the final branch.
- [ ] Update relevant user/operator documentation after behavior is stable.

## 6. API, schema, and dependency impact

Expected impact:

- Database migration: none.
- Pairing or WebSocket protocol change: none.
- Existing runtime API shape: unchanged.
- Existing daemon CLI command names: unchanged.
- New API surface: PowerShell installer endpoint.
- Existing download API: same endpoint, expanded OS allowlist and `.exe` file
  support.
- OpenAPI artifact: regenerated because the installer/download contract changes.
- New Go dependency: avoid one if the standard library is sufficient; if native
  Windows handles require `golang.org/x/sys/windows`, it is already present in
  the module graph and must be used consistently rather than adding a second
  Windows syscall wrapper.

## 7. Security and operational requirements

- Pairing tokens remain short-lived and single-use.
- Do not put tokens in download URLs, daemon command arguments, server access
  logs, PID files, or persistent installer output.
- PowerShell input must be quoted as data; do not interpolate an untrusted
  device name into executable code.
- The binary download endpoint must keep a strict OS/architecture allowlist and
  construct filenames from validated values only.
- The local Windows stop channel must be accessible only to the same user.
- Do not require elevation for the normal pairing flow.
- Do not write state, logs, or caches to the repository or current working
  directory.
- Preserve the existing daemon protocol version and fail-closed handshake.

## 8. Verification matrix

| Area | Linux | macOS | Windows x64 |
|---|---:|---:|---:|
| Existing Bash installer | Required | Required | Not applicable |
| PowerShell installer | Not applicable | Not applicable | Required |
| Self-hosted binary download | Required | Required | Required |
| GitHub Release fallback | Required | Required | Required |
| Foreground `connect` | Required | Required | Required |
| Background `connect -b` | Required | Required | Required |
| `status` / `logs` / `stop` | Required | Required | Required |
| Agent CLI discovery | Required | Required | Required for installed CLIs |
| Agent cancellation / cleanup | Required | Required | Required |
| CI build | Required | Cross-build | Native Windows job |

The implementation is complete only when Windows works and the existing Linux
and macOS rows remain green.

## 9. Decisions to confirm before implementation

1. Is Windows x64 sufficient for the first PR, with ARM64 explicitly deferred?
2. Is a native graceful-stop channel (named pipe) required, or is a bounded
   force-termination fallback acceptable for the first Windows release?
3. Which Agent CLI(s) must be included in the first Windows end-to-end test?
4. Should the UI use tabs or show two command blocks simultaneously?
5. Is serving the Windows binary from self-hosted server images required for
   the first release, or may the first PR rely only on GitHub Releases?

Resolving these decisions before coding keeps the feature to one coherent
cross-platform device-pairing change and prevents unrelated installer/service
work from entering the PR.
