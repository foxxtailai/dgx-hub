# Changelog

## Unreleased

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
