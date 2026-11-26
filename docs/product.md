# AXV product direction

This document owns identity, established direction, enduring product boundaries and open decisions. [Project status](../README.md#project-status) owns current availability; the [documentation map](README.md) routes implementation, API and usage references. Goals and open decisions below are not claims of implemented functionality.

## Identity

| Item | Convention |
| --- | --- |
| Product name | **AXV** in prose and the interface |
| Repository namespace | `cofy-x/axv` |
| Executable and project slug | `axv` |
| npm namespace | `@cofy-x/axv` and its workspace packages |
| Go module base | `github.com/cofy-x/axv` |
| Package description | A personal cloud workstation. |
| Tagline | **Your personal cloud for VMs and coding agents.** |
| Name interpretation | **Agents × Virtualization** |

**A** represents agents, **V** represents virtualization, and **X** connects them. Internal Nextstep names describe the desktop's design heritage and do not need a branding migration.

## Established direction

- **Single-machine personal cloud workstation:** Deployed on one person's Mac or Linux machine, combining VM management and host coding agents. Codex is the current implementation focus; Claude Code remains a core integration goal. Multi-host scheduling is outside scope.
- **NEXTSTEP / Window Maker desktop:** Preserve square gray controls, crisp bevels and overlapping windows while prioritizing readability, Unicode, keyboard access and touch ([Design](design.md)).
- **Online PWA client:** The deployment machine owns services, running work and storage. Browser installation provides an online standalone window without offline writes or automatic page replacement; optional Web Push registers a narrowly scoped notification worker only after explicit subscription. Browser support and native VM hypervisor support are independent deliverables.
- **Unified `axv` entry:** The installed `axv` binary composes independent desktop and compute services under one profile (`~/.axv`), keeping development profiles separate and preserving persistent work across restarts and updates ([Startup](startup.md)).
- **Local-first with optional private/tunnel remote access:** Local access is primary; embedded Tailscale is the private remote default, and optional Cloudflare Access supports browser access without a Tailscale client. Explicit project service publishing uses isolated browser cookie sites rather than granting project content desktop permissions ([Remote access](remote-access.md), [access boundaries](api/README.md#access-and-identity-boundaries)).
- **Desktop useful before VM setup:** VM preparation runs inside Virtual Machines with explicit review before downloads or installation, activating the verified service without restarting the desktop. Both desktop and `axv vm` share the same local host state, while local SSH/transfer/forwarding remain separate from remote desktop transport and agent-to-VM tools.

## Cloudflare service isolation and authorization

Cloudflare publications serve potentially untrusted VM application code and enforce four product invariants (exact enforcement, PSL/cookie proof and recovery live in [Cloudflare API](api/cloudflare.md)):

- **Dedicated cookie sites:** Every publication receives a server-generated hostname and permanently owns its browser cookie site (per PSL including PRIVATE rules), distinct from the desktop and every other publication including removed ones. Ordinary sibling subdomains are unsupported.
- **Bounded browser model:** Supports scripts, ordinary cookies, origin storage, HTTP, SSE and WebSockets; excludes service workers and offline execution.
- **Finite owner lease:** Owner access requires a signed Access JWT with `exp - iat` of at most 15 minutes; connections terminate at that original expiry without renewal on reconnect. Public visibility is separately reviewed and carries no owner lease.
- **Fail-closed withdrawal and restart:** Unpublish withdraws local access before cloud cleanup; desktop restart leaves retained publications closed until explicitly reviewed and resumed against their original target.

## Agent and VM responsibilities

- **Host-side official agents:** Managed coding agents run on the AXV deployment machine using official provider CLIs (retaining their own auth, approvals, sandbox and model settings). Running coding agents inside VMs is outside the current plan.
- **VMs as development environments:** VMs host project code, Git worktrees, dependencies, builds, tests and development services via the guest service. Authoritative VM project code lives on retained project volumes; AXV does not silently migrate or sync host projects with VM projects.
- **Explicit agent-to-VM boundary:** Host agents operate on VM projects through explicit AXV interfaces (`axv vm project` and the per-run bundled `axv-vm-project` Skill). A Skill guides the agent; it does not redirect built-in host tools or replace provider permissions.
- **Conversation lifetime:** Conversations outlive views; closing a view, ending a run and continuing saved context are separate user choices ([Codex architecture](codex.md)).

### VM project workspace constraints

Every VM project feature (files, initialization, persistent tasks, previews, backups and future providers such as QEMU/KVM) must preserve these invariants:

1. **Distinct identities:** Project (retained volume + relative root), environment binding (VM/boot/guest instance) and task attempt identities remain separate. VM replacement, remount or restore invalidates old bindings; recovery into another volume requires an explicit new project binding.
2. **Durable task acceptance:** Builds, installs and tests use persistent request IDs, task status, logs and supervision — separate from synchronous guest exec. Recover uncertain responses by querying the original receipt; retries never duplicate work.
3. **Attempt-preserving recovery:** Disconnects do not stop accepted tasks. Lost executors/VMs produce visible interrupted/unknown states; explicit retry creates a new attempt; cancellation addresses only the original VM, attempt and child process group.
4. **In-guest version-checked writes:** File access stays confined to the registered project root and rejects stale bindings, unsafe paths and hash conflicts. Draft replacement and conflict resolution require user choice. File confinement does not sandbox arbitrary guest commands.
5. **Honest resource reporting:** Report configured limits separately from observed usage and actual enforcement; expose unsupported capabilities (such as per-task disk/I/O/network throttling).
6. **Verified offline backups:** Backups copy stopped volumes with provenance; restoration creates a new volume and explicitly verifies root identity and Git/worktree metadata before binding a separate project ID. A VM snapshot alone is not a complete project backup.

Provider differences belong in capability declarations so clients only offer supported actions. Native verification of these constraints lives in [VM project workspace acceptance](acceptance.md#vm-project-workspace-acceptance).

## Intended workflow

1. Choose a project and its location: an existing host directory or a VM's persistent project volume.
2. Start or resume a host agent session with that project context explicit.
3. For a VM project, use AXV tools to read/edit files, run persistent tasks and inspect previews inside the selected VM; host projects keep their host context.
4. Review code, test results and services alongside the session and matching terminal.

Desktop users arrange multiple windows; a dedicated narrow-screen phone workflow (prioritizing machine selection, entering a session, reading progress and answering approvals) remains a separate goal beyond responsive desktop fitting.

| Area | Responsibility |
| --- | --- |
| Virtual Machines | VM inventory, lifecycle, resources and connection state |
| Agents | Host coding-agent sessions, progress, approvals and recovery |
| Project files | Authoritative host or VM project copy, addressed by location |
| Terminal | Interactive access to an explicit host or VM project context |
| Inspector | Context for the selected machine, project or session |

Use **Virtual Machines** for the application/window title, **VMs** for compact labels and **New VM** for creation. Do not add nonfunctional launchers to represent roadmap items.

## Product boundaries

- **Explicit execution context:** Agent process location, project location and command execution location are distinct. A missing VM never silently redirects work to the host.
- **Organization is not execution:** Desktop workspaces arrange windows; they do not select VMs or move agents into VMs.
- **Persistence is deliberate:** VMs, project volumes, agent conversations, process runs and windows keep separate [lifecycle contracts](architecture.md#codex-attachment-and-process-lifetime).
- **Evidence-based availability:** Implemented adapters, automated fixtures and native platform validation are distinct claims ([project status](../README.md#project-status)).
- **Default image policy:** Official versioned AXV enhanced images are the intended long-term default preparation source; advanced users may select custom images explicitly.

## Work notification and extension boundaries

- **Work notifications:** Optional Web Push and Pocket Alert adapters report minimal observed status and link back to authenticated original work. Observation never approves requests or controls execution; browser permission remains browser/OS-owned ([Notifications API](api/notifications.md)).
- **Application extensibility:** Built-ins remain compiled React features; installed extensions run in an opaque sandboxed Web runtime with private application data only and cannot override built-in IDs. Future resource capabilities must pass through existing feature owners with exact identities ([Extensions API](api/extensions.md)).

## Decisions still open

- Git/worktree management workflows on project volumes and additional VM providers (such as QEMU/KVM).
- Automatic session supervision after host boot, external/off-host project backup delivery and recovery policy after VM replacement.
- Development-service discovery and process supervision beyond explicitly registered previews.
- Additional coding-agent integrations, including Claude Code and structured agent interfaces.
- Broader installed-browser acceptance, dedicated phone workflows and cross-device reconnection experience.

Resolve open items in focused implementation tasks; keep task plans and review logs in task/PR history rather than adding separate roadmap or audit files.
