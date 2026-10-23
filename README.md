# AXV

[![CI](https://github.com/cofy-x/axv/actions/workflows/ci.yml/badge.svg)](https://github.com/cofy-x/axv/actions/workflows/ci.yml)

**Your personal cloud for VMs and coding agents.**

AXV is a personal cloud workstation for virtual machines and coding-agent sessions: **Agents × Virtualization**. A Window Maker-inspired browser desktop brings documents, host shells, Codex and VM management together. See [architecture](docs/architecture.md) for service and package boundaries.

## Project status

AXV is unreleased and experimental. Interfaces and AXV-owned development data may change; review the [storage baseline policy](docs/storage.md#desktop-database-baseline) before updating an existing profile. [Product direction](docs/product.md) describes the goals and open decisions; propose and discuss feature work in [Issues](https://github.com/cofy-x/axv/issues).

- **Desktop:** Working windows, four desktop workspaces, Dock, menus, Inspector, and responsive layouts; PWA installation is not implemented
- **Applications (experimental):** Application Manager, explicitly installed self-contained HTML extensions, sandboxed windows, private revision-checked data and browser draft recovery; the included Checklist example covers installation, saves, replacement and removal. Extensions have no document, host-command, agent or VM access
- **Documents:** Local virtual filesystem, Markdown editing/preview, explicit saves, revision checks and draft recovery; project/VM/conversation links, immutable saved snapshots and explicit exchange with registered host projects. Project Files browses host directories on macOS/Linux and retained VM projects; visible VM source previews follow external changes while preserving unsaved drafts. VM text editing uses reviewed file hashes and explicit environment bindings
- **Terminal:** Local host shells; disconnecting ends the shell, with no persistent session recovery
- **VM management (experimental):** Desktop and CLI lifecycle, configuration, images, volumes and task TTL; shared verified download cache with independent profiles. The default pinned Ubuntu base supports basic VM management during the transition to enhanced AXV images; official AXV image publication is pending. Shared-cache preparation and basic VM lifecycle are validated on macOS ARM64 with independent profiles. Native Linux amd64 Firecracker lifecycle, controller recovery, project retention, task TTL and network isolation are validated with a locally built image; scale remains unvalidated
- **Guest enhancements (experimental):** Protocol- and capability-gated execution, Guest Terminal, local SSH, SCP/SFTP, PTY, TCP forwarding, retained-volume project files and persistent project tasks. Tasks have durable receipts, independent attempts, logs, exact cancellation and recovery; capable Linux guests enforce CPU, memory, process, timeout and output limits. VM projects survive VM replacement on the same volume; stale bindings are rejected. Native enhanced-guest, project-file and persistent-task acceptance, including real repository installation/build/tests, resource failures, Git/worktree retention and VM replacement, is validated on Linux amd64 with a locally built Firecracker image; published AXV images and macOS enhanced-guest acceptance remain pending
- **VM project workflows (experimental):** Create empty projects or clone credential-free Git URLs through durable initialization tasks; explicitly register HTTP/WebSocket development previews on separate local browser origins; make offline whole-volume backups and restore into new volumes, VMs and verified project identities. Linux amd64 creation/clone, browser HTTP/WebSocket preview, controller/copy recovery and restored Git/worktree state are validated with a locally built Firecracker image. Remote preview browsing is unavailable; remote owners can inspect or stop local registrations. Stop revokes preview access while independently started services keep running; application sessions and bearer credentials remain confined to the preview. Local image candidates are not official published images
- **Startup:** Installable `axv` command, configuration-only `init`, background launch/reopen, one profile for persistent data, isolated development profiles, foreground `serve` and independent VM host activation; native Linux amd64 desktop supervision validated in a systemd user session
- **Coding agents:** Host Codex conversation recovery, names, archives and recent host projects on macOS/Linux; real-model continuity and saved-history recovery validated on Linux amd64. Structured per-run activity distinguishes working, pending approvals/input and turn outcomes; Action needed surfaces pending requests and exceptional outcomes, including archived sessions, with exact-event dismissals retained across desktop restart. Real-provider approvals/input, quiet completion, interruption and exact-event restart recovery are validated on Linux amd64; failed-turn handling is validated with the official CLI under a controlled transport fault. VM projects can open scoped Codex sessions with an AXV Skill and immutable per-run file/task tools; explicit continuation reviews the environment while preserving the conversation. Real-model VM file edits, builds and tests are validated on Linux amd64 with a locally built Firecracker image. Saved document snapshots can accompany the next submitted message in an exact run, with delivery receipts. Host-agent permissions remain separate. Claude Code remains unimplemented
- **Remote access (experimental):** Optional private HTTPS desktop through embedded Tailscale, with persistent node identity and owner authorization; disabled by default. The Tailscale utility enables, authorizes and stops the embedded node without restarting the workstation. [Remote setup](docs/startup.md#remote-access) keeps the system Tailscale service independent. Linux production HTTPS, owner access and terminal continuity are validated through an independently authenticated userspace client and physical iPhone Safari; physical tablet acceptance remains pending
- **Cross-platform:** Browser and host-terminal adapters exist; VM adapters are implemented for Linux/macOS; Windows VM hosting unavailable

## Install and use

Build from source with **Go 1.27.3**, **Node 24.16.0**, and **pnpm 12.11.2**. Background startup needs a macOS GUI login or a Linux systemd user manager (254 or newer); see [startup requirements](docs/startup.md#daily-commands). Otherwise, use `axv serve` after installation:

```bash
make install
axv
```

Installation builds the complete production executable and puts it in Go's binary directory (`GOBIN`, otherwise the first `GOPATH` entry's `bin`). Follow the printed PATH guidance if needed. The installed command works outside the repository without Node: `axv` starts or reopens the background workstation and opens its verified browser address. Configuration and AXV-managed data default to `~/.axv`.

[Startup and background services](docs/startup.md) covers installation, updates, [uninstallation](docs/startup.md#uninstallation), VM preparation, VM CLI and SSH usage, storage and service commands. [Using AXV](docs/usage.md) covers desktop applications and their session lifecycles.

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

## Contributing and support

Read [Contributing](CONTRIBUTING.md) for the contribution workflow and the [Support guide](SUPPORT.md) for bug reports, feature requests and usage questions. Report vulnerabilities privately through the [Security policy](SECURITY.md). The [Code of Conduct](CODE_OF_CONDUCT.md) applies the organization's community rules and supplies AXV's incident-reporting channels.

[Release preparation](docs/releasing.md) covers release checks, artifacts and third-party notices.

## License

AXV is licensed under the [MIT License](LICENSE).
