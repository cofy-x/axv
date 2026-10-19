# AXV

**Your personal cloud for VMs and coding agents.**

AXV is a personal cloud workstation for virtual machines and coding-agent sessions: **Agents × Virtualization**. A Window Maker-inspired browser desktop brings documents, host shells, Codex and VM management together. See [architecture](docs/architecture.md) for service and package boundaries.

## Project status

- **Desktop:** Working windows, four desktop workspaces, Dock, menus, Inspector, and responsive layouts; PWA installation is not implemented
- **Documents:** Local virtual filesystem, Markdown editing/preview, explicit saves, revision checks and draft recovery; project/VM/conversation links, immutable saved snapshots and explicit exchange with registered host projects. Project Files browses host directories on macOS/Linux; VM project-file browsing is not implemented
- **Terminal:** Local host shells; disconnecting ends the shell, with no persistent session recovery
- **VM management (experimental):** Desktop and CLI lifecycle, configuration, images, volumes and task TTL; shared verified download cache with independent profiles. The default pinned Ubuntu base supports basic VM management during the transition to enhanced AXV images; official AXV image publication is pending. Shared-cache preparation and basic VM lifecycle are validated on macOS ARM64 with independent profiles. Native Linux amd64 Firecracker lifecycle, controller recovery, project retention, task TTL and network isolation are validated with a locally built image; scale remains unvalidated
- **Guest enhancements (experimental):** Protocol- and capability-gated execution, Guest Terminal, local SSH, SCP/SFTP, PTY and TCP forwarding. Native enhanced-guest acceptance is validated on Linux amd64 with a locally built Firecracker image; published AXV images and macOS enhanced-guest acceptance remain pending
- **Startup:** Installable `axv` command, configuration-only `init`, background launch/reopen, one profile for persistent data, isolated development profiles, foreground `serve` and independent VM host activation; native Linux amd64 desktop supervision validated in a systemd user session
- **Coding agents:** Host Codex conversation recovery, names, archives and recent projects on macOS/Linux; real-model continuity and saved-history recovery validated on Linux amd64. Saved document snapshots can accompany the next submitted message in an exact run, with delivery receipts. Claude Code and AXV's agent-to-VM tooling/Skill integration are not implemented
- **Remote access:** Unavailable; authentication, authorization, remote session transport and Tailscale integration are not implemented
- **Cross-platform:** Browser and host-terminal adapters exist; VM adapters are implemented for Linux/macOS; Windows VM hosting unavailable

## Install and use

Build from source with **Go 1.26.8**, **Node 24.16.0**, and **pnpm 12.11.2**:

```bash
make install
axv
```

Installation builds the complete production executable and puts it in Go's binary directory (`GOBIN`, otherwise the first `GOPATH` entry's `bin`). Follow the printed PATH guidance if needed. The installed command works outside the repository without Node: `axv` starts or reopens the background workstation and opens its verified browser address. Configuration and AXV-managed data default to `~/.axv`.

[Startup and background services](docs/startup.md) covers installation, updates, VM preparation, VM CLI and SSH usage, storage and service commands. [Using AXV](docs/usage.md) covers desktop applications and their session lifecycles.

## Start developing

Use the same pinned toolchain from a writable checkout:

```bash
make deps
make dev
```

Open the Vite URL printed by the launcher. Development allocates independent loopback ports and uses a separate profile at `~/.axv/dev/<checkout-id>`. See the [development guide](docs/development.md#local-development) for prerequisites, `AXV_DEV_HOME`, builds and verification.

## Build and run

```bash
make build
./dist/axv serve
```

`make build` writes the production bundle to `dist/axv` without installing it. It embeds the UI. Guest services are built into VM images separately. `serve` runs the desktop in the foreground.

## Documentation

Contributors and coding agents follow [AGENTS.md](AGENTS.md), then use the [documentation map](docs/README.md) to choose the affected topic, its source entry points and its owning reference. The map covers architecture, APIs, design and product decisions without requiring a full-documentation read for each change.

## Verify changes

Follow [verification](docs/development.md#verification) for required commands, documentation-only checks, browser coverage and isolated test operation. The guide also covers native-daemon and explicit real-Codex acceptance.
