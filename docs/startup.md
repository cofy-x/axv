# Startup and background services

This guide owns installation, updates, uninstallation, CLI workflows, service operation, configuration, paths, download cache and host VM setup. AXV-managed configuration, data and runtime records default to `~/.axv`; `AXV_HOME` selects another profile root. See [remote access](remote-access.md) for Tailscale/Cloudflare setup, [development](development.md) for builds/tests, and [project status](../README.md#project-status) for platform support.

VMs run on the computer where AXV is deployed (Lima on macOS, Firecracker/KVM on Linux); the browser's OS never selects the VM host.

## Execution identity

AXV runs as the OS user that launches it (including root) without an additional flag. Host Terminal and coding agents inherit that user's permissions, and provider tools retain their own approval/sandbox policies. Default profile paths use that user's home directory; AXV never switches users or migrates another user's profiles or provider history. [Local access rules](api/README.md#access-and-identity-boundaries) apply to every launch identity.

## Installation and updates

### Install a release binary

Use the self-verifying installer script on macOS or Linux (verifies SHA-256 checksums before atomic installation):

```bash
curl -fsSL https://raw.githubusercontent.com/cofy-x/axv/main/scripts/install.sh | sh
```

### Install a release archive

Choose an **AXV application archive** (`axv_<version>_<os>_<arch>.tar.gz` for `linux_amd64`, `linux_arm64` or `darwin_arm64`) from [Releases](https://github.com/cofy-x/axv/releases) (independently versioned Guest images share the same catalog). Download the platform archive, `SHA256SUMS` and `provenance.json` into a new empty directory, verify [build provenance](releasing.md#verify-build-provenance) against the release's reviewed source SHA, then verify checksums and extract:

```bash
# Linux (linux_amd64 or linux_arm64):
axv_release_version='<version>'
axv_release_bundle="axv_${axv_release_version}_linux_amd64"
grep "  ${axv_release_bundle}.tar.gz\$" SHA256SUMS | sha256sum --check --strict - &&
  tar -xzf "${axv_release_bundle}.tar.gz" &&
  "./${axv_release_bundle}/axv" version

# macOS (Apple Silicon darwin_arm64):
axv_release_version='<version>'
axv_release_bundle="axv_${axv_release_version}_darwin_arm64"
grep "  ${axv_release_bundle}.tar.gz\$" SHA256SUMS | shasum -a 256 -c - &&
  tar -xzf "${axv_release_bundle}.tar.gz" &&
  "./${axv_release_bundle}/axv" version
```

The production executable embeds the Web UI and needs neither Go nor Node at runtime. Codex, tmux, OpenSSH, VM providers, kernels and Guest images are separate prerequisites (`axv doctor` inspects them). Review the [storage baseline policy](storage.md#desktop-database-baseline) before pointing a new release at an existing profile; test in a fresh `AXV_HOME` with `serve` when compatibility is unconfirmed.

### Build from source

From a checkout with the [pinned toolchain](../README.md#install-and-use):

```bash
make install
axv
```

`make install` installs locked dependencies, builds the production executable with embedded UI, and atomically installs it into `GOBIN` (or the first `GOPATH/bin`). Pass `GOBIN=/absolute/bin` for an explicit destination and `VERSION=1.2.3` for a custom version (default `dev`). It does not edit shell startup files, initialize profiles or start services.

### Apply an installed update

Replacing `axv` on `PATH` does not interrupt live services: running desktops capture immutable executable copies under `AXV_HOME/bin` for host and Codex children. Run `axv daemon restart` to apply the updated desktop binary (or `axv daemon upgrade` to sync installed desktop and Linux VM host services). Existing Codex runs retain their original executable and session identity.

### Browser installation

Install from a stable production origin (loopback on the deployment machine, or the enrolled [Tailscale HTTPS address](remote-access.md#tailscale) on another device; never a changing Vite dev URL):

- **Desktop Chrome / Edge:** Use the browser's install action in the address bar or menu.
- **iPhone Safari (via Tailscale):** Open AXV's HTTPS URL in Safari → **Share → Add to Home Screen** (with **Open as Web App** enabled).

The [manifest](../apps/dashboard/public/manifest.webmanifest) fixes `id`, `start_url` and `scope` at `/`, independent of `instanceId`, `launchId`, database ID and browser client ID. No service worker is registered by installation; opting into [browser notifications](usage.md#browser-push-notifications) registers a separate push-only worker. See [installed window behavior](usage.md#installed-window-and-updates).

## Uninstallation

Use the same OS account and `AXV_HOME` as the workstation being removed:

1. End any Codex runs you want terminated (**End Session**), and stop/delete VMs and volumes you want removed (`axv vm stop` / `axv vm rm`).
2. Stop managed background services with `axv daemon stop` (or `Ctrl+C` for foreground `axv serve`). On Linux, remove `axv-host.service` explicitly via `sudo axv daemon uninstall --vm` if installed.
3. Remove only the installed `axv` executable (check `command -v axv`; preserve the rest of its directory).

Removing the binary preserves profile data, VM storage, logs and the independent [shared download cache](#shared-download-cache). Never recursively remove `~/.axv`: it also holds the shared cache and `dev/<checkout-id>/` profiles. Stop all writers and back up wanted data before selectively deleting AXV-owned profile directories; host projects, provider credentials/history and unrelated tmux sessions remain separate.

## Daily commands

```bash
axv                              # start or reopen the workstation (prints URL/hints in SSH/headless sessions)
axv doctor [--json]              # inspect services, virtualization prerequisites, tools and stale artifacts
axv init                         # create a starter config.json without starting services or databases
axv serve                        # run the desktop in the foreground
axv daemon start [--enable] [--with-vm] [--only desktop|vm]
axv daemon status [--json]       # unified desktop, autostart, remote and VM host readiness
axv daemon logs [-f] [--desktop|--vm] [-n 100]
axv daemon restart [--all] [--only desktop|vm]
axv daemon stop [--all] [--only desktop|vm]
axv daemon enable | disable      # manage persistent boot/login desktop autostart
axv daemon upgrade               # sync this AXV binary to installed services (Linux VM host and/or desktop)
axv daemon uninstall [--all|--vm]
axv gc [--dry-run] [--json]      # clean up unreferenced pinned executables and stale launch records
```

- Running `axv` without arguments verifies and opens an existing healthy desktop (including foreground `serve`), or starts managed background services. Reuse does not apply edited config or shell environment.
- Default URL is `http://127.0.0.1:3001` (loopback peer and loopback Host required; port `0` allocates a free port). One desktop may use a profile/database at a time.
- Desktop readiness never waits for VM checks. Background supervision uses launchd (`~/Library/LaunchAgents/`) on macOS and systemd user units (systemd 240+, checking `loginctl` lingering) on Linux. In SSH/headless sessions, `axv` skips local browser launch and prints local/remote URLs and SSH tunnel hints. Where native supervision is unavailable, run `axv serve`.

| Operation | Lifetime effect |
| --- | --- |
| `daemon restart` | Replaces the desktop; ends Host Terminal shells, detaches Codex views, preserves managed VM host and guests |
| `daemon stop` | Stops this profile's managed desktop/host jobs; independent Codex runs, VMs and project volumes survive |
| Foreground shutdown | Stops the desktop and only the host child started by that invocation; reused or external VM services continue |

## VM command line

The VM CLI talks directly to the local host Unix socket; no browser or desktop service is required.

```bash
axv init
axv setup
axv vm images
axv vm create dev
axv vm list            # alias: vm ls
axv vm inspect dev
axv vm stop dev        # add --force for force-stop
axv vm start dev
axv vm rm dev [--yes]
axv vm volumes
```

- On macOS, the first VM command starts or reuses the managed Lima host under native supervision. On Linux, the administrator-managed `axv-host.service` must already be running (`axv host status|start|stop|restart|upgrade|uninstall` manages both).
- `vm create <name>` defaults to a started development VM with 2 CPUs, 1024 MiB RAM, 8 GiB root disk and a retained 4 GiB project volume (`--no-start`, `--cpus`, `--memory`, `--disk`, `--project-disk`, `--image <id>`, `--kind task --ttl 1h`, `--retain-volume`).
- Commands accept exact case-sensitive names or stable machine IDs. Mutations print profile, host, target and request ID to stderr before submission and wait by default (`--no-wait`, `--timeout 10m`). Interruption stops waiting without cancelling accepted work; recover uncertain mutations with the original `AXV_HOME` and `--host-id`:

```bash
axv vm operation list
axv vm operation get <operation-id>
axv vm operation wait <operation-id>
axv vm operation wait --request-id <original-request-id> --host-id <original-host-id>
```

Exit codes: `0` success, `1` operation failure, `2` usage error, `130` interrupted (SSH preserves remote exit status). Pass `--json` for machine-readable stdout.

### VM project files

`axv vm project` is the explicit host-agent interface for projects on retained VM volumes (requires a running VM whose guest confirms `project-files`):

```bash
axv vm volumes --json
axv vm project register --machine dev --volume <volume-id> --root repo --name 'My project' --json
axv vm project list --json
axv vm project binding <project-id> > project-binding.json
axv vm project files <project-id> '' --binding-file project-binding.json
axv vm project read <project-id> src/main.go --binding-file project-binding.json
axv vm project write <project-id> src/main.go --binding-file project-binding.json \
  --expected-sha256 <sha256-from-read> --file /absolute/reviewed-main.go
axv vm project mkdir <project-id> notes --binding-file project-binding.json
printf 'Project notes\n' | axv vm project write <project-id> notes/README.md \
  --binding-file project-binding.json --create
```

- Omit `--root` to register the volume root. Registration prints its receipt to stderr; for an exact replay after response loss, inspect `project list --json` first, or pass `--host-id`, immutable machine ID, `--volume-revision` and the original `--request-id` and fields together.
- Binding and file commands return JSON by default. File commands require the captured `{ project, binding }` file and never auto-refresh it or fall back to the host. After VM restart, replacement or guest restart, explicitly review and recapture a new binding.
- Paths are project-relative (`''` lists root; `files` supports `--limit`/`--cursor`). `read` returns UTF-8 text and SHA-256; `write` accepts up to 1 MiB (stdin by default, `--file -`) and requires exactly one of `--expected-sha256` or `--create`. Never blindly retry an uncertain write/mkdir; inspect the original path first. See [Compute project API](api/compute.md#vm-projects) for limits, [VM project backups](#vm-project-backups) for offline restore-to-copy binding, and [VM project tasks](#vm-project-tasks) for persistent commands.

### VM project initialization

Register the retained volume root as a parent workspace, capture its binding, then submit typed initialization (requires `project-files`, `project-tasks` and `project-initialization`):

```bash
axv vm project register --machine dev --volume <volume-id> --name 'Volume workspace' --json
axv vm project binding <parent-project-id> > workspace-binding.json
axv vm project initialize <parent-project-id> my-repository \
  --binding-file workspace-binding.json --request-id clone-repository-001 \
  --repository https://github.com/owner/repository.git --name 'My repository'
axv vm project task receipt <parent-project-id> clone-repository-001 \
  --binding-file workspace-binding.json
axv vm project finish-initialization <parent-project-id> clone-repository-001 \
  --binding-file workspace-binding.json
```

Omit `--repository` for an empty directory. URLs must be credential-free HTTPS or SSH (configure guest Git auth via [private repository authentication](usage.md#private-repository-authentication)). Existing destinations fail unchanged; failed clones preserve partial files and are not rerun — fix auth and initialize a new directory with a new request ID. After task success, `finish-initialization` explicitly registers the captured directory identity.

### VM project tasks

`axv vm project task` runs persistent installs, builds and tests inside a registered VM project (requires `project-tasks`; see [guest credentials](usage.md#credentials-for-project-tasks) before handling secrets):

```bash
axv vm project binding <project-id> > task-binding.json
axv vm project task submit <project-id> --binding-file task-binding.json \
  --request-id install-original --timeout 30m -- npm ci
axv vm project task receipt <project-id> install-original --host-id <original-host-id>
axv vm project task inspect <project-id> <task-id> --host-id <original-host-id>
axv vm project task list <project-id> --host-id <original-host-id>
axv vm project task logs <project-id> <task-id> <attempt-id> \
  --host-id <original-host-id> --offset 0 --limit 65536
axv vm project task logs <project-id> <task-id> <attempt-id> \
  --host-id <original-host-id> --offset 0 --raw > attempt.log
axv vm project task cancel <project-id> <task-id> <attempt-id> \
  --host-id <original-host-id>
axv vm project task retry <project-id> <task-id> \
  --binding-file retry-binding.json --request-id retry-original
```

- `submit` returns after durable acceptance without waiting for completion. Defaults: 15m timeout, 1 CPU, 512 MiB RAM, 64 processes, 1 MiB combined stdout/stderr (`--directory`, `--timeout`, `--cpus`, `--memory`, `--max-processes`, `--max-log-bytes`). Commands run as direct argv without shell interpolation (pass `sh -ec '<script>'` explicitly if needed).
- Submit and retry print the recovery receipt to stderr before sending; recover uncertain responses with `receipt` rather than submitting a new request. Queries accept `--host-id` or a saved `--binding-file`.
- `logs` returns one chunk (`nextOffset` continues paging; `--raw` writes exact bytes). `cancel` addresses one exact attempt using its recorded binding; `retry` creates a new attempt against a freshly reviewed binding only after the prior attempt is final (never while `unknown`).

### VM project backups

Save drafts, finish/reconcile all tasks and stop the source VM before creating an offline whole-volume backup (excludes VM root disk, guest task journal, host task history, Codex history and unsaved browser drafts):

```bash
axv vm project backup list --host-id <host-id>
axv vm project backup create <project-id> --host-id <host-id> \
  --request-id backup-original --name "Before upgrade" \
  --project-revision <project-revision> --volume-revision <volume-revision> \
  --machine <stopped-machine-id> --machine-revision <machine-revision>
axv vm operation wait --host-id <host-id> --request-id backup-original
axv vm project backup restore <backup-volume-id> --host-id <host-id> \
  --request-id restore-original --revision <ready-backup-revision> \
  --name "Recovered volume"
axv vm operation wait --host-id <host-id> --request-id restore-original
```

Create and start a compatible VM with `--volume <restored-volume-id>` (image/guest must declare `project-restore` and `project-files`), then explicitly bind a new project identity:

```bash
axv vm project backup bind <backup-volume-id> --host-id <host-id> \
  --request-id bind-original --source-project <original-project-id> \
  --volume <restored-volume-id> --volume-revision <current-volume-revision> \
  --machine <new-running-machine-id> --name "Recovered project"
axv vm project binding <new-project-id> > restored-binding.json
```

Preserve `--host-id`, `--request-id` and all original arguments when recovering uncertain backup/restore/bind calls. Old project IDs and agent run contexts remain attached to the original volume ([Compute backup API](api/compute.md#vm-project-backups)).

### Bound VM project tools for Codex

When starting Codex from a VM project's Project Files window, AXV creates a private run context and injects the bundled [`axv-vm-project` Skill](../apps/server/internal/vmwork/skills/axv-vm-project/SKILL.md) with an immutable bound command (`AXV_EXECUTABLE internal-vm-project RUN_CONTEXT COMMAND ...`). It exposes `files`, `read`, `write`, `mkdir` and `task` pinned to the recorded executable, project, environment, compute socket and configuration fingerprint — never loading another profile, activating a service or refreshing a stale binding. See [Codex VM work context](codex.md#vm-project-work-context).

### SSH, file transfer and forwarding

Use the system OpenSSH client over AXV's authenticated private host transport (no public TCP listener or guest-IP reachability required):

```bash
axv vm ssh dev
axv vm ssh dev -- uname -a
axv vm ssh dev --start
axv vm ssh dev -N -L 8080:127.0.0.1:3000
axv vm ssh dev -N -R 9000:127.0.0.1:9000
axv vm ssh dev -N -D 1080
axv vm ip dev --json
```

`axv vm ssh-config dev` prints a standalone OpenSSH config for `ssh`, `scp` and `sftp` without modifying `~/.ssh/config` (bound to profile, host identity and stable VM ID):

```bash
vm_config=$(mktemp)
axv vm ssh-config dev > "$vm_config"
vm_host=$(awk '/^Host / {print $2; exit}' "$vm_config")
ssh -F "$vm_config" "$vm_host" uname -a
scp -F "$vm_config" ./local.txt "$vm_host":/projects/
sftp -F "$vm_config" "$vm_host"
rm "$vm_config"
```

AXV stores a private Ed25519 client key and per-machine known-hosts pins under `AXV_HOME/ssh/`; personal SSH keys/agents are never imported. SSH sessions are connection-bound (no Guest Terminal replay/recovery) and excluded from remote desktop transports ([Guest SSH API](api/guest.md#ssh-access)).

## Work notifications and remote access

- **Work notifications:** Configure optional browser Web Push or Pocket Alert locally in **Notifications** (disabled by default; credentials live in `AXV_HOME/notifications/`). See [usage](usage.md#work-notifications) and [Notifications API](api/notifications.md).
- **Tailscale and Cloudflare:** See [Remote access and service publishing](remote-access.md) for Tailscale HTTPS, Cloudflare Access desktop setup, project service publishing and CLI commands (`axv remote`, `axv cloudflare`).

## First-time macOS setup

Launch `axv` and open **Virtual Machines** (or run `axv setup` in a terminal; pass `--yes` noninteractively and `--retry <task-id>` to resume an interrupted task):

- **Default image:** On Apple Silicon (`darwin/arm64`), standard setup defaults to the published, verified **AXV Lima arm64 Guest image** (`0.1.0-guest.3`, `--image-source official`), which preinstalls the AXV Guest service so new VMs support browser **Open Terminal**, SSH, and project tools once Guest is ready. Architectures without a published official AXV Lima image (such as Intel Macs) fall back to the pinned official Ubuntu 24.04 base (`--image-source ubuntu-base`, basic VM lifecycle with a local-only **Copy Lima Shell** action on running VMs), which also remains selectable explicitly on any Mac. Supply a locally built bundle via `--image-source bundle --image-bundle /absolute/bundle/manifest.json` ([image recipes](../images/guest/README.md)). Preparation never replaces existing VM image or volume bindings.
- **Private runtime:** Preparation installs and verifies **Lima 2.2.1** privately under the profile from the [shared download cache](#shared-download-cache) (preserving its Apache 2.0 license, independent of Homebrew/system Lima). Reviewed URLs and digests live in [`compute/artifacts`](../apps/server/internal/compute/artifacts).
- **Short socket paths:** macOS Unix socket limits constrain `LIMA_HOME`; choose a shorter `AXV_HOME` (`AXV_DEV_HOME` for dev) or a dedicated Lima directory in advanced settings if preparation reports an overlong path.
- **Durability:** Preparation runs in the background and survives browser close/refresh. Cancel is available before configuration commit; after commit, retry resumes activation without deleting existing VM storage.

## Linux privilege boundary

On Linux, the desktop connects over a local Unix socket (`/run/axv-host/control.sock`) to an independently supervised privileged host service ([`axv-host.service`](../config/examples/axv-host.service)).

### Install and connect the service

On Linux amd64, run the command shown in **Virtual Machines** (or with `command -v axv` resolving to the intended executable):

```sh
sudo "$(command -v axv)" host install-service --apply-sysctl
```

- Checks systemd 254+, KVM, TUN, cgroup v2, IPv4 forwarding and host tools (`--check` runs prerequisites read-only).
- Downloads and verifies pinned Firecracker/jailer, the [default AXV Guest image and matching kernel](../apps/server/internal/compute/artifacts/firecracker.go) under `/opt/axv`, writes `/etc/axv/host.json`, enables and starts `axv-host.service` (`--no-enable` skips boot autostart), persists `net.ipv4.ip_forward=1` in `/etc/sysctl.d/99-axv.conf`, and connects the invoking user's profile (`--no-connect` skips; `--socket-group NAME` sets a shared control group).
- Manage the installed service via `axv daemon status`, `axv daemon logs --vm -f`, `sudo axv daemon start|stop --only vm`, `sudo axv daemon upgrade`, `sudo axv daemon uninstall --vm` (or `sudo axv host ...`).
- Connect a profile manually with `axv setup --socket /run/axv-host/control.sock` or `axv setup --host-config /etc/axv/host.json`.

### Custom Linux service

For other architectures, custom paths or manual provisioning, adapt [`host-firecracker.json`](../config/examples/host-firecracker.json) and [`axv-host.service`](../config/examples/axv-host.service):

- **Prerequisites:** KVM, TUN, cgroup v2 (`cgroup.kill` + delegated `cpu`, `memory`, `pids`), `ip`, `nft`, `cp`, `mkfs.ext4`, `resize2fs`, IPv4 forwarding and applicable host firewall rules ([VM networking](vm-platform.md#host-firewall-integration)).
- **Mount execution check:** Jailer executes Firecracker inside `dataDir/firecracker/jails`; verify the mount lacks `noexec` (`findmnt -n -o TARGET,OPTIONS -T /var/lib/axv`).
- **Delegated cgroup:** Set `firecracker.cgroupParent` to the service's delegated cgroup path relative to `/sys/fs/cgroup` (e.g. `system.slice/axv-host.service` with `Delegate=yes`, `DelegateSubgroup=controller` and `KillMode=process` so VMs survive controller restarts).

### Linux Guest bundle installation

To install an additional or specific published Firecracker Guest release manually (rather than the default pinned by `host install-service`):

1. Download `axv_guest_<version>_firecracker_amd64.tar.gz`, `candidate.json`, `SHA256SUMS` (and `provenance.json` for attested releases) from [Releases](https://github.com/cofy-x/axv/releases) into a fresh directory. Verify [build provenance](releasing.md#verify-build-provenance) and `SHA256SUMS`, extract the bundle, and verify the bundle's internal `SHA256SUMS` and `manifest.json` against `candidate.json`.
2. Copy the complete verified `bundle/` (retaining `distribution/`) into a versioned directory such as `/opt/axv/images/<version>/` alongside an independently verified [compatible kernel](acceptance.md#linux-firecracker).
3. Append an entry to `images` in `/etc/axv/host.json` using the exact `id`, `name`, `provider`, `arch`, `rootfs` path/`rootfsSHA256`, `guest` contract (including `binarySHA256`) from `manifest.json`, plus your `kernel` path/`kernelSHA256`.
4. Restart the host controller (`sudo systemctl restart axv-host.service`) and reconnect desktop profiles (`axv setup --socket /run/axv-host/control.sock`) so the new configuration fingerprint is verified.

For a brand-new manual service installation without `host install-service`, install the binary (`/opt/axv/bin/axv`), `/etc/axv/host.json` (`0600`) and `/etc/systemd/system/axv-host.service` (`0644`), then `systemctl daemon-reload && systemctl start axv-host.service`.

## Configuration and storage

Example combined `AXV_HOME/config.json`:

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

Compute modes are `disabled`, `managed` (absolute Lima `host_config`) or `external` (absolute local Unix `socket` for an independently supervised local host). Unknown profile keys are preserved; malformed JSON or unsupported versions fail without resetting files.

| Setting | Precedence (highest first) |
| --- | --- |
| Listen address | `--addr` → `ADDR` → `127.0.0.1:PORT` → `desktop.addr` → `127.0.0.1:3001` |
| Desktop data | `--data-dir` → `DATA_DIR` → `desktop.data_dir` → `AXV_HOME/data` |
| Compute connection | `AXV_COMPUTE_SOCKET` (read-only override) → profile `compute` |
| GitHub Review VM | `AXV_REVIEW_VM_CONFIG` (per-repo JSON) → `AXV_REVIEW_VM_{HOST,MACHINE,PROJECT}_ID` ([recipe](../recipes/github-review/README.md)) |

| Path under `AXV_HOME` | Content |
| --- | --- |
| `config.json`, `host.json` | Shared profile and macOS generated host config |
| `data/` | `desktop.sqlite`, `extensions.sqlite`, `github-review/reviews.sqlite`, Codex run manifests |
| `notifications/` | Private notification state and credentials (`state.json`) |
| `cloudflare/` | Optional published-service journal (`publications.json`) |
| `workspace/` | Default host project directory (created on first use) |
| `images/`, `compute/`, `lima/`, `tools/lima/` | Profile VM images, `compute.sqlite`, private Lima state and private Lima binary |
| `preparation/` | Preparation task database (`preparation.sqlite`) and staging |
| `host/control.sock`, `ssh/`, `tailscale/` | Managed host socket, profile SSH key/known-hosts, embedded Tailscale credentials |
| `run/`, `bin/` | Locks, launch snapshots, daemon ownership (format **4**), private Codex sockets, pinned executables |
| `logs/` | Rotating `daemon.log` (10 MiB × 5, `0600`, sanitized JSON lines) plus supervisor `desktop.log`/`host.log` |
| `dev/<checkout-id>/` | Independent development profiles ([development](development.md#local-development)) |

All SQLite stores and journals follow the [experimental baseline policy](storage.md#desktop-database-baseline): incompatible formats fail visibly and are never silently reset. Stop all writers (including live Codex runs) before taking filesystem backups or resetting an AXV-owned store.

`axv daemon logs` prints bounded tails of `daemon.log`, `desktop.log` and `host.log` (or systemd journald logs for `--vm` on Linux). Host processes write structured diagnostics to stderr; `daemon.log` redacts credentials and omits terminal streams, file contents and raw bodies. For an isolated preview:

```bash
AXV_HOME="$PWD/.preview/profile" ADDR=127.0.0.1:0 /absolute/path/to/axv serve
```

## Shared download cache

`AXV_CACHE_HOME` selects the shared download cache (default `~/.axv/cache`, independent of `AXV_HOME`, `AXV_DEV_HOME` and `AXV_TEST_TMPDIR`; no fallback when set explicitly). Cache directories require mode `0700` and current-user ownership; completed artifacts live at `artifacts/v1/sha256/<full-sha256>/artifact` (`0444`). Profiles receive independent copies (no symlinks or hard links into the cache).

```sh
axv cache list
axv cache verify --json
axv cache fetch ubuntu-arm64          # or ubuntu-amd64 / lima-arm64 / lima-amd64
axv cache import lima-arm64 /absolute/file.tar.gz
axv cache remove <full-sha256>
```

`fetch` and `import` accept `--output /absolute/file`. Busy entries report `in_use` and refuse removal; there is no automatic eviction.

## Download mirrors and guest package sources

- **Artifact download mirrors:** Set `AXV_ARTIFACT_MIRRORS` to a JSON object mapping full lowercase SHA-256 digests to exact HTTPS URLs (no credentials, queries or fragments). Matching digests use the mirror only with **no automatic fallback**; partials bind to their source URL + digest (remove an inactive partial via `axv cache remove <sha256>` before switching mirrors). Restart the desktop daemon for new plans to pick up environment changes.
- **Guest APT sources:** Explicit selection supports Ubuntu **24.04** (`arm64`/`amd64`) with `official` or `aliyun` while preserving Ubuntu archive keyring `Signed-By` verification ([image build option](../images/guest/README.md#package-source-selection)). For an existing running Lima VM:

```sh
axv vm package-source <name-or-id> --source aliyun
axv vm package-source <name-or-id> --source official
```

This durable operation requires the current VM revision, backs up existing APT sources under `/var/lib/axv/package-sources-*`, writes deb822 config + cloud-init preservation policy, and does not run `apt-get update` or upgrade packages. Runtime Firecracker source maintenance is unsupported.

## Host agent defaults

- `AXV_WORKSPACE_DIR` overrides profile `workspace_dir` (default `AXV_HOME/workspace`), created on first New Session.
- Proxy precedence: existing proxy env vars → profile `proxy_url` (`http`, `https`, `socks5`, `socks5h`) → enabled macOS system proxies.
- New Codex runs use the default tmux server unless `AXV_TMUX_SOCKET` selects another; recorded routing persists per run and never alters global tmux options/environment ([Codex routing](codex.md#tmux-routing-and-environment)).

## Agent settings and background lifecycle

Foreground and background desktops resolve the same workspace, proxy and tmux settings. Background launches capture a scoped environment allowlist (PATH, home/user/shell/locale, Codex/OpenAI settings, proxies, CA paths and review VM selectors — see [architecture](architecture.md#startup-and-service-ownership)) into a private (`0600`) launch snapshot.

Editing `config.json` or shell variables never mutates a running desktop: use the Tailscale/Cloudflare panels to apply remote toggles immediately, or run `axv daemon restart` to capture updated desktop/agent settings while preserving the compute host and existing Codex runs. On Linux, desktop units retain surviving tmux processes, so a prior unit may remain visible until those processes exit. See [`daemon/native.go`](../apps/server/internal/daemon/native.go) and [native daemon smoke](acceptance.md#native-daemon-smoke).
