# Changelog

## Unreleased

- Double-clicking the exe or shortcut opens the dashboard: the first desktop launch starts it and opens the browser, while later launches reuse the running instance and exit before opening storage or starting SSH/tray work. Existing older DGX Hub instances are recognized; unrelated port occupants still report an error. Headless launches do not open a browser.
- Larger centered sidebar logo without a duplicate app name, plus a transparent PNG favicon with a content-based URL to avoid old browser icons. An explicit Shell cache repair handles legacy exe icons that survive ordinary refreshes.
- Supplied transparent DGX Hub fox artwork replaces the application, tray, browser favicon and dashboard brand icons. Windows resources include nine sizes from 16 to 256 pixels; Windows builds notify Explorer to refresh the exe's cached file icon.
- ConnectX status on node cards, with automatically identified peer nodes, interface pairs and negotiated speeds. Bounded interface-bound IPv4/IPv6 checks distinguish a verified peer from carrier-only, disconnected, undetected and unavailable states; stale or deleted peers cannot remain connected.
- Device Temp and Device Power on node cards, with independent N/A handling and stale readings preserved after SSH failure.
- Dashboard refresh pauses while any settings or Actions window is open, ignores cancelled requests and resumes once on close. Action execution and live logs continue independently.
- Frontend regression tests cover refresh timing, cancellation, form state and independent action polling; both build scripts run them.
- Windows Terminal tabs inherit the node's saved color when opening an SSH session.
- Five saved node colors (light green, blue, orange, red and yellow), shown on node cards, service node names, SSH setup and Actions. Existing nodes default to light green; changing only the color preserves active work.
- Shared Node Actions catalogue with reusable node assignments and an ordered multiline command editor.
- Sequential noninteractive SSH execution with per-step timeout, stop/continue policy, live output, cancellation and one active action per node.
- Persistent run history with command/name snapshots, exit codes and bounded stdout/stderr; interrupted runs recover as Cancelled.
- Actions panels match the dashboard theme and include confirmation, history details and log copying. Existing terminal, monitoring and tunnel checks remain covered.

## 0.1.2 — 2026-10-27

- Local Go application with embedded React/TypeScript UI and SQLite storage.
- Node and service management, live SSH telemetry, and shared-memory display for DGX Spark.
- Automatic loopback SSH tunnels with reuse, recovery, port-conflict handling, and cleanup.
- Windows tray, terminal launching, SSH key checks and step-by-step setup help.
- Dark English interface with a green-plus icon and a separate string dictionary.
- Windows build scripts and optional Linux backend cross-builds; Linux desktop integration remains deferred.
