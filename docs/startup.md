# Startup and background services

This guide owns installation, updates, uninstallation, CLI workflows, service operation, configuration, paths and environment. AXV-managed configuration, data and runtime records default to `~/.axv`; `AXV_HOME` selects another profile root. See [development](development.md) for builds and tests, and [project status](../README.md#project-status) for platform validation.

VMs run on the computer where AXV is deployed. Its operating system determines the VM runtime: Lima on macOS or Firecracker/KVM on Linux. The browser's operating system does not choose the execution machine.

## Execution identity

AXV runs as the operating-system user that launches it, including root, without an additional flag. Host Terminal and coding agents inherit that user's permissions; provider tools retain their own approval and sandbox policies. Default profile paths use that user's home directory. AXV does not switch users or migrate another user's profiles, provider configuration or history. The [local access rules](api/README.md#access-and-identity-boundaries) apply to every launch identity.

## Installation and updates

From a checkout with the [pinned toolchain](../README.md#install-and-use):

```bash
make install
axv
```

`make install` installs locked build dependencies, builds the production executable with its Web UI, then atomically installs it into the effective Go binary directory. The configured `GO` command supplies `GOBIN`; when unset, the destination is the first `GOPATH` entry's `bin`. For an explicit destination, use `GOBIN=/absolute/bin make install`. Installation accepts native builds only and retains the previous executable if building or publishing fails. `VERSION=1.2.3 make install` sets the displayed version; development builds otherwise report `dev`.

Follow the printed PATH guidance if the directory is missing or another `axv` takes precedence. Installation does not edit shell startup files, initialize profiles, start services or prepare VMs. The installed command works outside the checkout and needs no Node at runtime. `make build` only creates `dist/axv`; `make deps` only installs build dependencies.

Reinstalling replaces the global entry without interrupting live services. Both foreground and background desktops capture immutable executable copies under `AXV_HOME/bin` for their host and Codex children. Reopening with `axv` retains the current service version and settings. Use `axv daemon restart` to apply the installed desktop version; explicit stop/start updates both managed desktop and host services. Existing Codex runs keep their original executable and session identity.

## Uninstallation

Use the same operating-system account and `AXV_HOME` as the workstation you intend to remove. Other profiles and independently supervised services have separate lifetimes.

1. Before stopping services, choose **End Session** for each [Codex run](usage.md#codex) you want to terminate, and stop the selected VMs in **Virtual Machines** or with `axv vm stop <name|id>`. If you also want to remove VM storage, use the normal machine and volume deletion controls while the host is available; review [project-volume retention](usage.md#virtual-machines) first.
2. For managed background jobs, run `axv daemon stop` for this profile. Stop a foreground `axv serve` in its launching terminal with Ctrl+C. Daemon shutdown retains independent Codex runs, VMs and project volumes; an administrator-managed Linux host service requires its own supervisor action. Confirm the intended services have stopped before removing their executable or data.
3. Remove only the installed `axv` executable (`axv.exe` on Windows) at the destination printed by `make install`. In a POSIX shell, `command -v axv` helps check which entry is on `PATH`; review that path before removing it. Keep the containing Go binary directory and its other programs.

Removing the installed command leaves profile configuration, documents, default workspace, VM storage, logs and fixed executable copies in place. Reinstallation can reuse retained state under the [baseline policy](storage.md#desktop-database-baseline). For optional data cleanup, back up wanted content, stop all affected writers and review the [resolved profile and storage paths](#configuration-and-storage) before selectively removing resources. Existing host projects, provider-owned credentials/history and unrelated tmux sessions remain outside this cleanup; keep run ownership records until their processes have stopped.

The [shared download cache](#shared-download-cache) is independent of a profile. If you want to reclaim cached downloads, use `axv cache list` and `axv cache remove <full-sha256>` before removing the command. Do not recursively remove the entire default `~/.axv`: it also contains that cache and independent development profiles. Separately installed Codex, tmux, OpenSSH and administrator-managed VM runtimes are maintained through their own installation methods.

## Daily commands

Use the installed command from any directory:

```bash
axv                      # start or reopen the background workstation in a browser
axv init                 # create a starter config without starting services
axv serve                # foreground desktop; VM service starts in the background
axv daemon start         # explicit background start without opening a browser
axv daemon status        # desktop identity and VM controller readiness
axv daemon logs          # print recent service logs
axv daemon restart       # reload desktop settings, preserving the VM host
axv daemon stop          # stop this profile's managed services
```

Running `axv` without arguments first verifies the profile's existing desktop and opens its address, including when that desktop was started with `serve`. Reuse does not apply edited configuration or a different shell environment. With no healthy instance, it starts managed background services. Concurrent launches wait for the same owned startup; an unresponsive instance reports diagnostics instead of being automatically replaced. A browser-opening failure prints the usable URL and leaves the service running.

Use `axv help` and each command's `--help` for options; help and version commands do not initialize storage. Put command-specific options after the command. `daemon start` and `restart` accept `--open`, `--addr` and `--data-dir`. `axv init` creates a private starter `config.json` only when missing, with desktop defaults and compute disabled. It preserves an existing profile and does not create databases, a project workspace or services.

The default URL is `http://127.0.0.1:3001`. An occupied port is an error; port zero allocates a free port and prints the actual URL. Explicit repeated `daemon start` verifies both instance identity and requested desktop settings before reuse. One desktop may use a profile/database at a time. The daily opener verifies only a local HTTP address and never follows redirects or proxies its identity request.

The desktop becomes available before VM environment checks finish. Missing VM dependencies, invalid compute settings and an unavailable VM service are reported in **Virtual Machines** without preventing document and host-session use. Desktop configuration, database and listener errors still fail startup. Already configured managed services are started or verified for reuse in the background; this does not start stopped VMs.

macOS uses launchd in the current GUI login session. Linux uses a systemd user manager with `systemd-run` (systemd 254 or newer). Starting a daemon does not enable login/boot startup or alter lingering. Where native background supervision is unavailable, including Windows, use foreground `axv serve`; AXV does not silently switch supervision methods.

| Operation | Lifetime effect |
| --- | --- |
| `daemon restart` | Replace an existing desktop; end ordinary host shells, detach Codex views, preserve managed VM host and guests |
| `daemon stop` | Stop this profile's managed desktop/host jobs; Codex runs, VMs and project volumes survive |
| Foreground shutdown | Stop the desktop and only the host child this invocation started; reused or independently managed VM services stay running |

Interrupting a launch before desktop readiness removes only the desktop job started by that invocation. Reused desktops and independently supervised VM hosts retain their ownership and lifetime.

Persistent work still consumes resources after services stop. Use **End Session** for Codex and **Virtual Machines** to stop/delete VMs. Task-expiry enforcement pauses while the controller is down. A host restart observes existing VMs and resumes queued work; it does not boot previously stopped machines. Guest-shell lifetimes are described in [usage](usage.md#virtual-machines).

## VM command line

The VM CLI talks directly to the same local host service as the desktop. A browser and desktop service are optional:

```bash
axv init
axv setup
axv vm images
axv vm create dev
axv vm list
axv vm inspect dev
axv vm stop dev
axv vm start dev
```

On macOS, the first VM command starts or reuses the configured managed host under native supervision, without opening a browser or creating the desktop database. Compute configuration is resolved independently of desktop address, workspace and agent proxy settings. An external Linux service must already be running under its administrator's supervisor. Service startup never starts an existing stopped VM.

The host may be the profile's only managed job. `daemon status`, `logs` and `stop` work in that state. Running `axv` or `daemon start` adds a desktop while preserving the host; `daemon restart` requires an existing desktop and does not create one.

`vm create <name>` creates and starts a development VM by default. Use `--no-start` to create it stopped. Web and CLI share host-provided creation defaults: 2 CPUs, 1024 MiB memory, an 8 GiB root disk and a retained 4 GiB project volume. The selected image can require a larger root disk; capacity failures are reported without reducing the request. `--cpus`, `--memory`, `--disk` and `--project-disk` override those allocations. Select `--image <id>` when more than one compatible image is available. `--kind task --ttl 1h` creates a task VM with disposable project storage by default; add `--retain-volume` to retain its project volume after deletion or expiry. [Machine creation](api/compute.md#machine-creation-and-configuration) owns resource limits and the unique-name contract.

VM commands accept the exact case-sensitive name or stable machine ID. `vm list` has the alias `vm ls`; `vm images` and `vm volumes` list their respective inventories. `vm start`, `stop`, `restart` and `rm` operate on one VM. `stop --force` requests provider-supported force-stop. `rm` asks for confirmation, or accepts `--yes` (`-y`) for noninteractive use; it deletes the root disk and honors the recorded project-volume retention policy.

Mutation commands print the profile, host, target and request ID before submission and the accepted machine/operation IDs to stderr. They wait for completion by default; `--no-wait` returns after acceptance and `--timeout 10m` changes the wait bound. Interruption or timeout stops waiting without cancelling accepted work. Do not repeat a mutation while its outcome is unknown:

```bash
axv vm operation list
axv vm operation get <operation-id>
axv vm operation wait <operation-id>
axv vm operation wait --request-id <original-request-id> --host-id <original-host-id>
```

An unknown request ID is not proof that submission failed; `operation wait --request-id` continues checking until it finds a final result or times out. Keep the original `AXV_HOME` and pass the receipt's `--host-id` to operation get/wait so recovery fails if the host was replaced. `--json` produces machine-readable stdout while diagnostics remain on stderr. Inspect and operation get/wait already return JSON by default. Exit codes are **0** for success, **1** for operation failure, **2** for usage errors and **130** for interruption; SSH also preserves the remote command's exit status.

### SSH, file transfer and forwarding

Use the installed system OpenSSH client for standard guest sessions:

```bash
axv vm ssh dev
axv vm ssh dev -- uname -a
axv vm ssh dev --start
axv vm ssh dev -N -L 8080:127.0.0.1:3000
axv vm ssh dev -N -R 9000:127.0.0.1:9000
axv vm ssh dev -N -D 1080
axv vm ip dev --json
```

SSH requires a running VM; only `--start` explicitly starts a stopped one. `--timeout` bounds preparation/start, not an established connection. `-t` requests a PTY (repeat to force it), `-T` disables one, and `-N` runs forwarding without a remote command. Local (`-L`), remote (`-R`) and SOCKS (`-D`) forwarding use OpenSSH semantics; remote listeners remain on guest loopback. SSH commands preserve the remote exit status. Guest addresses from `vm ip` describe the current guest network, not guaranteed host reachability; SSH uses the private host transport instead of depending on those addresses.

`ssh-config` prepares access and prints an independent configuration for `ssh`, `scp` and `sftp`. It also requires a running VM unless `--start` is supplied, and does not edit `~/.ssh/config`. For example:

```bash
vm_config=$(mktemp)
axv vm ssh-config dev > "$vm_config"
vm_host=$(awk '/^Host / {print $2; exit}' "$vm_config")
ssh -F "$vm_config" "$vm_host" uname -a
scp -F "$vm_config" ./local.txt "$vm_host":/projects/
sftp -F "$vm_config" "$vm_host"
rm "$vm_config"
```

Generated configuration binds the profile, host identity, host configuration and stable VM ID. It remains usable after that VM is renamed or restarted, but fails if the host binding changes or the VM is deleted; a new VM with the same name is a different target. Each attachment verifies the current boot before opening the stream. Direct `vm ssh` also fixes the boot selected during preparation. Neither path silently replaces a missing target or starts a stopped VM from a saved configuration.

AXV creates a private Ed25519 client key and known-hosts file under `AXV_HOME/ssh/`. It registers only that profile's public key with the selected guest and pins the guest's key by host and machine identity. A changed host key fails and requires explicit investigation/repair of that exact known-hosts entry. Personal SSH keys, agents and configuration are not imported. SSH uses the guest `axv` account with its normal filesystem permissions; it is separate from detachable browser Guest Terminal and has no AXV session recovery. These commands provide local guest access; remote access to AXV still requires a separate authentication and authorization design.

See the [Guest API](api/guest.md#ssh-access) for transport and key-registration limits.

## First-time macOS setup

```bash
axv
```

Open **Virtual Machines**. The environment workflow detects dependencies, lets you choose storage and allocation budgets, and shows the exact preparation actions before you start them. Until AXV enhanced images are published, the default is the pinned official Ubuntu 24.04 base: basic creation, start, stop, restart and deletion remain available without AXV guest enhancements. The reviewed plan names that base and its capability limit, verifies its immutable artifacts, publishes the host configuration and verifies the managed service in the same desktop process. This is the public transition policy; publication will require a separate explicit default-policy change. Selecting `--image-source official` currently reports `release_pending` with no invented URL. Advanced settings accept a local custom bundle. Preparation does not create or start a VM.

Automatic preparation uses a separately installed and verified **Lima 2.2.1** under each profile, reusing that profile's matching installation when present. It copies the pinned official archive from the shared cache and extracts privately, without selecting, changing or sharing Homebrew/system installations. Explicit existing host configurations retain their administrator-selected runtime pins. The complete [official Lima release](https://github.com/lima-vm/lima/releases/tag/v2.2.1), including its Apache 2.0 license under `share/doc/lima/LICENSE`, is preserved in that private installation. The reviewed [artifact catalog](../apps/server/internal/compute/artifacts) owns release URLs and digests; the shared [preparation engine](../apps/server/internal/compute/prepare) owns plans and execution. VZ hosting needs supported macOS and the system SSH tools; a blocked prerequisite is reported before downloads begin.

Storage defaults to the profile's `images/`, `compute/` and `lima/` directories. macOS Unix sockets impose a short path limit: preparation reports an overlong profile instead of moving Lima state elsewhere. Choose a shorter `AXV_HOME` (`AXV_DEV_HOME` for development), or explicitly select a dedicated Lima directory in advanced settings. Review the resolved paths before proceeding. CPU, memory, disk and machine-count budgets limit admission rather than reserving physical capacity. Leave headroom for macOS and image copies. Existing configurations keep their storage paths and provider ownership.

Preparation runs in the background. Closing or refreshing the window does not cancel it. Failed or interrupted work retains its task and verified artifacts; use the visible retry action. Cancellation is available before configuration commit. After commit, service-start failures retain the configuration so a retry can continue activation. An existing unrelated file, directory or socket is never permission to reset or adopt it.

`axv setup` remains available for terminal use and shares the same preparation and validation rules. With a running desktop, it follows or submits a task to that verified instance. Without a desktop, it prepares configuration only and reports that the service has not started. Review the displayed plan before confirming; noninteractive preparation requires `--yes`. `axv setup --retry <task-id>` retries the original interrupted or failed task with its original settings. `axv setup --help` lists storage, capacity and local-image options. Preserve `AXV_HOME` when selecting a nondefault profile.

Guest enhancements require preinstalled services from the [image build recipes](../images/guest/README.md). For custom preparation use `axv setup --image-source bundle --image-bundle /absolute/bundle/manifest.json`. VM Start initializes machine identity but does not install packages, replace guest executables or upgrade images. See the [image and capability contract](vm-platform.md#image-and-capability-contract) for compatibility checks.

## Linux privilege boundary

Provision Firecracker as an administrator-managed system service on the same Linux computer where AXV runs, using the [host example](../config/examples/host-firecracker.json), [systemd unit](../config/examples/axv-host.service) and [guest image recipe](../images/guest). Replace digest placeholders and choose local paths, capacity, subnet and jail UID/GID ranges; the example cannot launch unchanged. Automatic Linux provisioning is not implemented.

The host requires KVM, TUN, cgroup v2 with `cgroup.kill` and delegated `cpu`, `memory` and `pids` controllers, pinned Firecracker/jailer binaries, `ip`, `nft`, `cp`, `mkfs.ext4` and `resize2fs`. Enable IPv4 forwarding separately and choose a short host data path and non-overlapping guest subnet. Existing host firewalls must also authorize guest forwarding; [VM networking](vm-platform.md#host-firewall-integration) owns coexistence with Docker and other firewall managers.

Set `firecracker.cgroupParent` to the service's delegated cgroup path relative to `/sys/fs/cgroup`, for example `system.slice/axv-host.service`. The example unit requires systemd 254 or newer: `Delegate=yes` delegates that subtree, and `DelegateSubgroup=controller` places the host process in its own leaf so VM resource controllers can be enabled beside it. AXV configures only its owned descendants inside that delegation. It does not enable controllers in global ancestor cgroups. Keep the unit name and configured parent stable while its VMs exist; `KillMode=process` preserves guests across controller restarts. Alternative supervisors must provide the equivalent writable, empty delegated parent and place the controller outside that parent or in a separate leaf.

Start AXV with the intended desktop account on that computer, then open **Virtual Machines** to configure and check the service's local Unix socket or readable host configuration. A successful connection becomes available in the current desktop without a restart. Missing permissions or an unavailable service show diagnostics and a check-again action; AXV never starts or stops the administrator's system service. Running the desktop as root does not change Firecracker's independent service ownership or enable managed Firecracker hosting.

The equivalent CLI configuration is:

```bash
axv setup --socket /run/axv-host/control.sock
```

Alternatively, `setup --host-config /etc/axv/host.json` reads a host configuration and saves its local Unix socket. The desktop needs existing socket access.

## Configuration and storage

A profile can combine desktop, agent and compute settings:

```json
{
  "runtime_version": 1,
  "workspace_dir": "/absolute/host-projects",
  "proxy_url": "http://127.0.0.1:7890",
  "desktop": {
    "addr": "127.0.0.1:3001",
    "data_dir": "/absolute/desktop-data"
  },
  "compute": {
    "mode": "managed",
    "host_config": "/absolute/axv-profile/host.json"
  }
}
```

Omitted desktop fields use defaults. Compute modes are `disabled`, `managed` with an absolute Lima `host_config`, or `external` with an absolute local Unix `socket`. The internal name `external` means that another supervisor owns the VM service process on this same computer. Setup preserves unrelated and unknown profile keys. The [profile reader](../apps/server/internal/profile/read.go) validates known fields; malformed JSON and unsupported runtime versions fail without resetting files.

The shared [desktop resolver](../apps/server/internal/config/config.go) applies startup overrides and resolves desktop storage, workspace, proxy and tmux settings from that profile. It validates requested settings before an initial profile is published and rereads the published profile so concurrent initialization preserves the winning configuration. [VM command configuration](../apps/server/internal/app/compute_settings.go) remains independent of desktop and agent settings.

| Setting | Precedence, highest first |
| --- | --- |
| Listen address | `--addr` → `ADDR` → `127.0.0.1:PORT` → `desktop.addr` → `127.0.0.1:3001` |
| Desktop data | `--data-dir` → `DATA_DIR` → `desktop.data_dir` → `AXV_HOME/data` |
| Compute connection | `AXV_COMPUTE_SOCKET` for an independently managed local VM service → profile `compute` |

Empty environment variables are unset; explicit flag values cannot be empty. Start/restart overrides are not saved to the profile. Repeat them on the next invocation or persist the settings explicitly. The environment workflow can configure and activate a previously unconfigured VM service without restarting the desktop. Existing provider/storage replacement remains an explicit maintenance operation with the original ownership preserved. Desktop and agent changes can use restart. `AXV_COMPUTE_SOCKET` is shown as a read-only launch override; writing the profile does not replace it.

| Location under AXV_HOME | Content |
| --- | --- |
| `config.json` | Shared profile |
| `host.json` | Host configuration generated by macOS setup |
| `data/` | Desktop SQLite database, layouts, Codex run manifests and AXV session records |
| `workspace/` | Default real host project directory, created on first use |
| `images/` | Independent verified image copies used by this profile |
| `compute/`, `lima/` | Compute state and default private Lima resources |
| `tools/lima/` | Verified private Lima releases, when needed |
| `preparation/` | Preparation task database, plans, progress and private preparation files |
| `host/control.sock` | Managed host API socket |
| `ssh/` | Private profile SSH client key and pinned guest known-hosts file |
| `run/` | Locks, readiness, ownership, immutable configuration snapshots and private Codex sockets |
| `bin/` | Fixed executable copies |
| `logs/daemon.log`, `logs/daemon.log.1`–`.5` | Private structured desktop/preparation log and bounded rotated archives |
| `logs/desktop.log`, `logs/host.log` | Native supervisor output, including structured host diagnostics |
| `dev/<checkout-id>/` | Independent development profiles; see [development](development.md#local-development) |

Desktop data defaults to `AXV_HOME/data` on every platform. Documents live in `data/desktop.sqlite`, separate from configuration and real project files. Previous development directory layouts are not detected, migrated or used as fallbacks. Startup rejects incompatible databases; the [baseline contract](storage.md#desktop-database-baseline) permits deleting obsolete AXV-owned development data after stopping its writers and affected Codex runs.

Daemon ownership records use format **4**, allowing separate host and optional desktop jobs; older formats are rejected without rewriting them. Database formats belong to the [desktop baseline](storage.md#desktop-database-baseline) and [compute/preparation owners](vm-platform.md#ownership). Do not remove ownership records while their processes remain live or edit format markers to bypass validation.

The unified root contains AXV-owned data, not copies of every dependency or project. Existing host projects, provider-owned authentication/history and the shared tmux server retain their own locations. Explicit desktop/VM storage overrides remain supported; independently administered Linux VM services retain their configured paths. Backups must include these external locations when used. Stop writers before taking a filesystem backup, including independent Codex runs when copying their records and SQLite sidecars.

The desktop operational log is `AXV_HOME/logs/daemon.log`. Foreground and native desktops, offline setup and cache mutations append JSON lines with timestamps, severity, process role, event names and available job/operation/request IDs. Preparation phases, throttled transfer progress and desktop connection failures share this profile sink. Service composition also routes standard Go log messages through the sanitizing logger. It does not record terminal/Codex streams, guest session output, document contents, request bodies or complete configuration/environment snapshots. Credential values and URL authentication, queries and fragments are redacted; individual diagnostics and records are bounded.

The log directory is private (`0700`) and log/lock files are private regular files (`0600`) on macOS and Linux. Writers to `daemon.log` share an interprocess lock; each append reopens the active file under that lock and rotates at 10 MiB, retaining at most five archives. Unsafe links, owners or permissions are rejected. Desktop startup, offline setup and cache mutations fail clearly when their sink cannot initialize; later write failures go to stderr and the desktop environment reports `loggingError`, without undoing successful database changes. The environment exposes the initialized desktop sink as `logPath`; the browser does not derive it.

All host processes write structured diagnostics to stderr. Native launchd/systemd supervision captures this output in `logs/host.log`; a foreground desktop forwards its managed host child's stderr to its own stderr. Independently administered hosts use their own supervisor's logging. Hosts do not infer a desktop log destination from `AXV_HOME` or append to `daemon.log`. Windows foreground desktops also use structured stderr logging and omit `logPath`; AXV does not claim Unix permission bits provide private Windows files.

`axv daemon logs` prints a bounded tail of `daemon.log` followed by available `desktop.log` and `host.log` tails, including output from existing services. These native role logs are not automatically rotated. Role logs, snapshots and executable copies are not automatically pruned. Stop services and preserve needed ownership/configuration records before maintaining profile files. Development and test launchers own independent profiles; never target a real profile in tests. For an isolated preview:

```bash
AXV_HOME="$PWD/.preview/profile" \
  ADDR=127.0.0.1:0 /absolute/path/to/axv serve
```

## Shared download cache

`AXV_CACHE_HOME` selects the download cache. When unset, AXV resolves the operating-system user's home directory and uses `~/.axv/cache`, independently of `AXV_HOME`, `AXV_DEV_HOME` and `AXV_TEST_TMPDIR`. An explicit absolute path (or `~/...`) uses only that location; there is no fallback to the default. Plans and CLI diagnostics show the effective cache root, verified hits and remaining downloads.

The cache owns `format.json`, an initialization lock and `artifacts/v1/`. Each complete payload is `artifacts/v1/sha256/<full-sha256>/artifact`, with source metadata beside it. Cross-process locks live in `artifacts/v1/locks/` and are never unlinked during maintenance. Directories must be real, current-user-owned and mode `0700`; completed payloads are regular immutable `0444` files. Unsupported formats, unsafe permissions, ownership conflicts and corrupt contents fail visibly without replacement.

The catalog includes pinned official Lima archives and Ubuntu base cloud images; future published AXV images use the same cache. User uploads, project files, VM disks and snapshots are not automatically cached. Image selection follows the [setup policy](#first-time-macos-setup).

```sh
axv cache list
axv cache verify --json
axv cache fetch ubuntu-arm64
axv cache import lima-arm64 /absolute/known-profile/preparation/downloads/lima-<sha256>.tar.gz
axv cache remove <full-sha256>
```

Use `lima-amd64` or `ubuntu-amd64` on Intel systems. Import accepts only a reviewed public artifact ID and verifies its fixed digest; it preserves the source. AXV does not scan user directories. `fetch` and `import` optionally accept `--output /absolute/profile-file` to create an independent copy. `list` and `verify` hash completed entries; busy entries report `in_use`. Explicit removal refuses active downloads, imports and profile copies, and handles only recognized cache-owned files. Corruption errors include the exact removal command; repair is explicit. There is no automatic eviction.

Each profile retains its own databases, images, VM root/project disks, Lima state, installed tools, processes, SSH keys and ownership. Extraction/copy happens under its original managed paths with no shared directory symlinks or cache/profile hard links. Removing a cached artifact does not affect an already prepared profile. Profile cleanup must preserve the cache: never recursively remove an entire `AXV_HOME`, especially the default `~/.axv`, which also contains the independently owned cache and development profiles. Stop and remove only the intended profile's owned resources.

## Download mirrors and guest package sources

These are independent selections. Neither changes the shared cache's digest key or a prepared VM's image contract.

**Artifact downloads:** `AXV_ARTIFACT_MIRRORS` is a JSON object mapping a reviewed artifact's **full lowercase SHA-256** to an **exact HTTPS download URL**. Load operator-verified locations with `export AXV_ARTIFACT_MIRRORS="$(cat /absolute/artifact-mirrors.json)"`. It applies to Web preparation, `axv setup`, `axv cache fetch`, development and explicit native acceptance. Unlisted digests use their catalog URL. AXV ships no guessed third-party artifact mirror addresses; package mirrors do not imply that the same site hosts cloud disks or Lima archives.

A matching override selects that mirror only, with **no automatic fallback**. Plans display selected and original locations; confirmation freezes the URL, architecture, version and digest. Transport, HTTP and checksum failures remain visible. Clear/change the override and review a new plan to change sources; retries keep the original plan. URLs must omit credentials, query strings and fragments. HTTPS, redirect count/scheme, size and checksum rules still apply. Warm entries are verified and reused regardless of download location. Partials bind to the exact source URL and digest; a source mismatch is rejected with an explicit removal path, never appended. Use `axv cache remove <sha256>` for the inactive partial before selecting a different source. Restart the desktop daemon to capture an environment change for new plans.

**Guest APT sources:** explicit selection supports only Ubuntu **24.04**, **arm64/amd64**. ARM64 uses Ubuntu Ports (`ports.ubuntu.com/ubuntu-ports` or `mirrors.aliyun.com/ubuntu-ports`); amd64 uses Ubuntu archive/security or `mirrors.aliyun.com/ubuntu`. All selections retain the Ubuntu archive keyring via `Signed-By`; they never disable signature verification. The [image recipes](../images/guest/README.md#package-source-selection) own builder options and dependency installation.

For an existing running Lima VM, including a basic Ubuntu VM without AXV guest software:

```sh
axv vm package-source <name-or-id> --source aliyun
axv vm package-source <name-or-id> --source official
```

This explicit durable maintenance operation changes only that VM's writable disk, requires its current revision, and locks against concurrent VM actions. It requires preinstalled Python 3. It backs up `sources.list`, `ubuntu.sources` and any existing AXV cloud-init source policy under `/var/lib/axv/package-sources-*`, writes deb822 configuration and removes the superseded active `sources.list`. The cloud-init policy preserves the selected sources across boots. Repeating an already applied selection makes no new backup; an interrupted operation can resume. Write failures attempt to restore the original files; backups remain available if recovery also fails. Other active `.list`/`.sources` layouts, symlinks, edited AXV cloud-init policies, unsupported releases/architectures, missing TLS trust roots and missing signing keyrings are rejected rather than rewritten. Source changes do **not** refresh package indexes, install packages or upgrade the system. No source operation occurs during Start. Runtime Firecracker source maintenance is unsupported; select sources in its explicit image build or administer the guest directly. Shared originals, other VMs and other profiles are untouched.

## Host agent defaults

`AXV_WORKSPACE_DIR` overrides profile `workspace_dir`, whose default is `AXV_HOME/workspace`. AXV creates this real host directory when New Session first needs it; virtual documents remain separate.

Existing proxy environment variables take precedence over `proxy_url`, then enabled macOS system proxies. The profile supports `http`, `https`, `socks5` and `socks5h`. Diagnostics show a source and redacted address, not verified model connectivity.

New runs use the default tmux server unless `AXV_TMUX_SOCKET` selects another. AXV passes the current environment to each new run and persists its actual routing. Later settings changes do not redirect old runs or change global tmux options/environment. [Codex architecture](codex.md#tmux-routing-and-environment) owns run routing details.

## Agent settings and background lifecycle

Foreground and background services resolve the same workspace, proxy and tmux settings. Background startup captures a scoped environment: PATH, home/user/shell/locale, Codex/OpenAI settings, proxies and certificate paths. The exact allowlist lives in [environment.go](../apps/server/internal/app/launch/environment.go); other shell variables are not copied, and empty captured values clear supervisor defaults.

The [`app/launch`](../apps/server/internal/app/launch) package owns captured desktop inputs, the persisted `Snapshot` contract and an explicit `Inputs` fingerprint. The runtime `app.Settings` composition adds live compute settings, logging and process dependencies without defining the snapshot protocol or desktop identity. Capture and restoration clone mutable snapshot values so later runtime changes cannot alter saved inputs. Restoring a background desktop uses its captured inputs and environment; later VM activation resolves the current compute configuration without changing the desktop's launch identity.

Native service snapshots also carry a launch identity, distinct from the process instance identity, so concurrent launchers can recognize their own job without adopting a foreground desktop. The shared profile owns immutable executable copies under `AXV_HOME/bin`; desktop composition captures one for later host and Codex children. Replacing the installed entry therefore cannot change an existing desktop's child executable. The [launch contract](../apps/server/internal/app/launch/snapshot.go) and [settings composition](../apps/server/internal/app/settings.go) are the implementation entry points.

Snapshots and ownership records are private (0600) and may contain supplied environment credentials. AXV does not read or copy Codex credential files or put snapshot values in supervisor command arguments/status output.

Editing a profile or shell environment does not mutate a live process. `daemon restart` captures updated desktop settings/environment for new sessions while preserving the compute host and existing Codex runs. On Linux, desktop units deliberately retain surviving tmux processes; a prior unit may remain visible until those processes exit. See [daemon supervision](../apps/server/internal/daemon/native.go) and [native daemon verification](development.md#native-daemon-smoke) when changing this behavior.
