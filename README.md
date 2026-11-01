# DGX Hub

DGX Hub is a local dashboard for DGX Spark nodes. It shows live Linux metrics, opens an SSH terminal, and opens saved web services through managed loopback SSH tunnels.

The current desktop target is **Windows 10/11 amd64**. Linux stays separate in the platform layer; its desktop integration is deferred. See `.localFiles/TESTING.md` for what was actually verified.

[Changelog](CHANGELOG.md) · [Verification report](.localFiles/TESTING.md) · [Project specification](.localFiles/DGXHub_Spec.md)

On the first normal launch, DGX Hub imports the existing `%LOCALAPPDATA%\FoxHub\foxhub.db` into `%LOCALAPPDATA%\DGXHub\dgx-hub.db`. The import includes committed SQLite WAL data, preserves the original as a backup, and never replaces an existing DGX Hub database. SSH keys and agent configuration continue to use the same Windows account. With `--data-dir`, only a legacy `foxhub.db` in that same custom directory is eligible for import; default-profile data is kept separate.

## Run on Windows

1. Download or build `dist/dgx-hub.exe`. It is a standalone binary with the React UI embedded; Node.js and Go are only needed for building.
2. Enable **OpenSSH Client** in Windows Optional Features if `ssh -V` does not work. The client is needed on this PC; installing an SSH server on this PC is unnecessary.
3. Run `dgx-hub.exe`. It stays in the background and adds a **green plus** icon to the notification area. Windows may place it in the hidden-icons overflow.
4. Right-click the icon and choose **Open DGX Hub**. The default address is `http://127.0.0.1:8787`.
5. Add your nodes using the same target you use with `ssh`, such as `spark_1` or `user@192.168.1.10`. No nodes or services are added automatically.
6. Choose **SSH setup** on each node card. DGX Hub checks background access and shows step-by-step help when keys or host-key trust need attention.
7. Add a service with its remote host and port. For an application listening on Spark_1's loopback interface, use `127.0.0.1` as the **remote host**. **Open** creates or reuses an SSH tunnel and opens a new browser tab.

Closing the browser leaves DGX Hub and its tunnels running. Choose **Exit** in the tray menu to stop DGX Hub and close its managed background SSH processes. Interactive terminal sessions belong to the user and can be closed normally.

## SSH configuration and first connection

DGX Hub uses the current Windows user's system OpenSSH, SSH configuration, keys, and agent. It does not ask for passwords in the browser or store keys/passwords in its database.

Optional aliases in `%USERPROFILE%\.ssh\config`:

```sshconfig
Host spark_1
    HostName 192.168.1.10
    User your-user
    IdentityFile ~/.ssh/id_ed25519

Host spark_2
    HostName 192.168.1.11
    User your-user
    IdentityFile ~/.ssh/id_ed25519
```

Replace these example addresses and usernames with your own. Existing aliases, including configured ports and jump hosts, can be reused. Background operations reject aliases with preconfigured `LocalForward`, `RemoteForward`, or `DynamicForward` directives, automatic SSH detachment, or other settings that prevent managed fixed commands. Use a separate alias without those directives for DGX Hub; its setup check explains the conflict. This keeps every DGX Hub tunnel local and under DGX Hub's control.

Connect once in your ordinary terminal (`ssh spark_1`), compare the host-key fingerprint with a trusted value from the node administrator, and complete initial trust. DGX Hub never turns off host-key checking.

For background access, create or reuse an SSH key and install its **public** `.pub` line in `~/.ssh/authorized_keys` on the remote account. Protect the remote `.ssh` directory with mode `700` and `authorized_keys` with `600`; preserve existing entries. Use a protected key with the system agent where appropriate. On Windows, enabling the `ssh-agent` service initially can require an Administrator PowerShell:

```powershell
Set-Service -Name ssh-agent -StartupType Automatic
Start-Service ssh-agent
```

Then load the key from your normal user terminal:

```powershell
ssh-add "$env:USERPROFILE\.ssh\id_ed25519"
ssh -o BatchMode=yes -o StrictHostKeyChecking=yes spark_1 true
```

The final check must finish without an interactive password, passphrase, or host-key prompt. DGX Hub's guide provides these steps and a retry button. It does not automatically install keys or change the node's SSH configuration.

## Terminals and services

Windows Terminal is preferred and opens an explicit new, visible window. An early activation failure triggers the native OpenSSH-console fallback and is recorded in `dgx-hub.log`. Commands use separate process arguments; a PowerShell command string is not constructed. If both launch methods fail, the UI shows the error and a copyable `ssh <target>` command.

Each service records the endpoint **as seen by its SSH node**. Two services on different nodes can use the same remote port. Local tunnel ports are allocated dynamically, kept only for this run, and bound to `127.0.0.1`. Editing or deleting a service closes its previous tunnel. Deleting a node also deletes its saved services after confirmation.

HTTP and HTTPS are supported. DGX Hub does not bypass HTTPS certificate checks, rewrite application URLs, or handle application authentication and WebSocket traffic. HTTPS services must have certificates suitable for the local URL or their own appropriate setup. DGX Hub checks remote TCP reachability and tunnel readiness without interpreting HTTP responses.

RAM is shown once as shared system memory; GPU activity is separate and displays **N/A** if unsupported. CPU is calculated from two `/proc/stat` snapshots. Memory uses `MemTotal - MemAvailable`; disks on the same mount are shown once. Failed polls retain previous readings marked stale.

## Node Actions

Choose **Actions** beside **Terminal** on a node card, then **Create action**. Set a name and optional description, enter one or more commands, and select the nodes that should share the definition. Actions belong to a central local catalogue: **Assign** adds an existing action to another node, and editing it updates the shared definition. An action can also remain unassigned in the catalogue. No example actions are installed automatically.

Each action supports up to 50 steps. Commands use multiline monospace fields; the arrow buttons change their order. Every step has a timeout (60 seconds by default, configurable from 1 to 86400) and an error policy. **Stop sequence** is the default; **Continue to next step** permits later commands after a failed command or timeout. The overall result still reports Failed if any step failed. SSH connection/setup failures always stop the sequence; OpenSSH exit code 255 is treated conservatively as a connection failure.

**Run** is available only when the selected node has current Online metrics. Enable **Ask for confirmation before running** to review the commands and destination before launch. Only one action can run on a node at a time; different nodes can run independently. Opening, editing, assigning, and saving an action never runs its commands. The regular terminal, telemetry and service tunnels use their existing independent lifecycle.

Each step uses a fresh system OpenSSH process with the same SSH aliases, keys, agent and strict host-key verification as other background operations. The remote command runs in `/bin/sh`, starting in `$HOME`, with a fixed environment: `HOME`, `USER`, `LOGNAME`, `PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`, and `LANG=C`. Shell profiles are not loaded, and `cd`, exported variables and shell state do not carry to the next step. Set custom environment variables or combine related operations inside one command when needed. Standard input is closed and no terminal is allocated; use noninteractive commands such as `sudo -n` so missing privileges produce an error instead of a password prompt.

The execution screen shows each command, status, elapsed time, exit code, stdout and stderr. Active output refreshes approximately every half second. **Copy logs** copies the complete retained result. Close the panel and reopen Actions to find an active run, or use **Node history** / an action's **History** to inspect earlier results.

**Cancel run**, a step timeout, node editing/deletion, and application exit close the managed local SSH operation. Remote processes that detach or ignore the closed SSH session may continue; cancellation cannot guarantee their termination. Check the remote state before retrying administrative work after a timeout or connection failure. Closing only the browser or Actions panel leaves execution running.

Definitions and run snapshots are stored in the existing local `dgx-hub.db`; SSH passwords and keys are never copied into them. Commands and their output are saved as entered/generated, so keep credentials in the existing SSH agent/key setup. History preserves action/node names and command results at launch even after a definition or node is deleted. It retains the latest 100 completed runs globally, plus active runs. Each stdout/stderr stream is limited to 64 KiB and combined output to 1 MiB per run; truncation is displayed. Command snapshots, JSON encoding and SQLite add storage overhead to these raw-output limits. History lists load summaries, with detailed logs retrieved only when opened.

Snapshots are saved before execution, at step boundaries, and on completion or normal cancellation. An abrupt process exit can lose the latest output from the current step; the next launch marks interrupted runs Cancelled instead of leaving them Running. If history cannot be saved, execution stops before starting further commands and reports the storage error. Actions provide explicit manual sequences; scheduling, branching and shared workflow state are outside this version.

## Data and diagnostics

- Windows: `%LOCALAPPDATA%\DGXHub\dgx-hub.db` and `dgx-hub.log`.
- Future Linux backend use: `$XDG_DATA_HOME/dgx-hub`, or `~/.local/share/dgx-hub`.
- No cloud account, public listener, remote exporter, or new service on a Spark is required.
- Only the canonical loopback dashboard URL is accepted. Mutations require same-origin requests and a process-local CSRF token. Service launch links carry a separate temporary token and reject cross-site initiation.

Optional command-line flags:

```text
dgx-hub.exe --port 8787
dgx-hub.exe --data-dir C:\path\to\custom-data
dgx-hub.exe --no-tray
```

`--no-tray` is for development/test runs. The release binary has no console window; use a console build below when you need Ctrl+C. The API always binds to `127.0.0.1`; a flag cannot expose it to the LAN.

Common problems:

- **SSH setup required**: configure keys, load protected keys into the agent, then use **Check again**.
- **Host-key verification**: verify the host identity in your terminal. A changed key needs investigation and a deliberate update to your SSH trust configuration.
- **Node offline**: verify SSH target, network/VPN reachability, and that the node's SSH server is running.
- **Service fails to open**: check that its remote host/port is correct and the service is running on that node. A live SSH connection alone does not prove a service is listening.
- **GPU N/A**: GB10/driver combinations may not expose utilization through `nvidia-smi`; DGX Hub keeps other metrics usable.
- **No icon or page**: check hidden tray icons and `dgx-hub.log`; another DGX Hub instance or program may already occupy port 8787.

## Build

Prerequisites: Go **1.26+**, Node.js **22.12+**, npm, and system OpenSSH. SQLite uses a pure-Go driver; a C compiler is unnecessary.

```powershell
.\build.ps1
```

The script builds the React UI, runs Go tests, and writes `dist/dgx-hub.exe`. `-SkipInstall` reuses an existing `web/node_modules`. Go caches stay inside `.cache/`; a local SDK at `.tools/go/bin/go.exe` is recognized when Go is absent from PATH.

Generated UI files, Windows resources, SDKs, caches and release binaries are excluded from Git. A fresh checkout must build `web/dist` before direct Go tests/builds; `build.ps1` performs these steps in order. Keep `go.sum` and `web/package-lock.json` in the repository.

Console development build, after building the UI:

```powershell
go build -o dist/dgx-hub-console.exe ./cmd/dgx-hub
.\dist\dgx-hub-console.exe --no-tray
```

Optional portable-backend cross-compilation:

```powershell
.\build.ps1 -Targets linux-amd64,linux-arm64
```

On Linux, `sh ./build.sh` builds the amd64 backend. Run it with `--no-tray`. Linux tray and terminal launching are deliberately deferred, and a successful cross-build does not establish Linux desktop support.

DGX Hub's browser must be on the same PC as DGX Hub: service redirects point to that PC's localhost. Running the backend on a headless Spark and using a remote browser is outside the supported deployment model.

## Project layout

`cmd/dgx-hub` composes the application. `internal/api`, `actions`, `storage`, `validation`, `sshutil`, `telemetry`, `tunnels`, and `platform` contain the corresponding small components. `web/src/strings.ts` keeps English UI copy separate for future localization.
