# Documentation map

Use this page to choose context and find the owner of a fact. [AGENTS.md](../AGENTS.md) owns repository-wide working rules; [project status](../README.md#project-status) records current availability. Read the relevant sections and follow their source links rather than loading every reference.

## Choose context

**Understand the project:** Read the [project README](../README.md) introduction and relevant status entries, then [ownership and dependencies](architecture.md#ownership-and-dependencies) and the [lifetime comparison](architecture.md#codex-attachment-and-process-lifetime). Read [product direction](product.md) when the task concerns goals, scope or open decisions. Use a feature reference only to go deeper into that area.

**Change a feature or fix a bug:**

1. Before application changes, read the compact [ownership overview](architecture.md#ownership-and-dependencies), then choose one primary owner from the task table below. Documentation-only changes start with the [existing content owner](#content-ownership) and its implementation links.
2. Read the owner's affected sections and implementation map, where provided. Follow its source links and use `rg` within those directories to trace the affected caller, handler/service and persistence or execution path. Inspect nearby tests before editing. For API work, the [API implementation map](api/README.md#find-the-implementation) locates route adapters and domain owners.
3. Add the [cross-cutting context](#cross-cutting-context) only when that boundary is affected. Choose the required checks from [verification](development.md#verification), implement within the existing owner, and update changed documentation using [Maintain the map](#maintain-the-map).

Start editing once you can identify the owner, affected call/state path, identities and lifetimes to preserve, and required checks. Expand the reading scope when the implementation reveals another affected boundary.

## Read by task

Choose the row for the behavior being changed. The primary reference owns the mechanism and source links; additional references are conditional.

| Task | Start here | Add when affected |
| --- | --- | --- |
| Applications, windows, menus, workspaces or layout | [Desktop implementation map](desktop.md#implementation-map) | [Desktop state API](api/desktop.md) for serialized state |
| Installed applications, packages, sandbox sessions or private app data | [Installed application API](api/extensions.md) | [Desktop architecture](desktop.md) for catalog and mounted windows; [extension storage](storage.md#installed-application-storage) for database ownership |
| Virtual documents, saves, revisions or recovery | [Document implementation map](storage.md#implementation-map) | [Files API](api/files.md); [database identity](api/desktop.md#database-identity-and-local-recovery) for browser recovery |
| Project browsing, links, snapshots or copies | [Storage ownership](storage.md#references-snapshots-and-project-exchange) | [Project files and exchange API](api/project-files.md) |
| Host shells or project terminals | [Terminal ownership](desktop.md#terminal-sessions) | [Host Terminal API](api/host-terminal.md) |
| Shared host directory selection | [Host directories API](api/host-directories.md) | The consuming Terminal, Codex or Project Files reference |
| Codex conversations, runs or document delivery | [Codex implementation map](codex.md#implementation-map) | [Host Codex API](api/host-codex.md) |
| VM project Codex sessions or bound AXV Skill tools | [VM work context](codex.md#vm-project-work-context) | [Bound tools](startup.md#bound-vm-project-tools-for-codex), [VM project contract](api/compute.md#vm-projects) and [project task contract](api/compute.md#vm-project-tasks) |
| VMs, volumes, images or guest execution | [VM implementation map](vm-platform.md#implementation-map) | [Compute API](api/compute.md), [Guest API](api/guest.md), [VM CLI](startup.md#vm-command-line) or [image recipes](../images/guest/README.md), according to the changed boundary |
| VM projects, retained-volume bindings or guest project files | [VM projects](vm-platform.md#vm-projects) | [Compute project API](api/compute.md#vm-projects), [guest file enforcement](api/guest.md#project-file-protocol), [VM project CLI](startup.md#vm-project-files); [product constraints](product.md#vm-project-workspace-constraints) for scope |
| VM project creation, Git cloning or initialization recovery | [Project initialization](vm-platform.md#project-initialization) | [Compute initialization API](api/compute.md#vm-project-initialization), [VM project CLI](startup.md#vm-project-files) |
| Offline project backups, restoration or explicit copy binding | [Project backups](vm-platform.md#project-volume-backups) | [Compute backup API](api/compute.md#vm-project-backups), [guest file enforcement](api/guest.md#project-file-protocol) |
| VM development-service previews or their remote access | [Development previews](api/project-previews.md) | [VM architecture](vm-platform.md#development-previews), [access boundaries](api/README.md#access-and-identity-boundaries) |
| Persistent VM project tasks, logs, cancellation or recovery | [Task ownership](vm-platform.md#persistent-project-tasks) | [Compute task API](api/compute.md#vm-project-tasks), [guest execution](api/guest.md#project-task-protocol), [task CLI](startup.md#vm-project-tasks), [native acceptance](development.md#vm-project-workspace-acceptance) |
| Installation, uninstallation, configuration, startup or supervision | [Startup](startup.md) | [Runtime identity API](api/desktop.md) for identity contracts |
| Private remote access, Tailscale enrollment or authorization | [Tailscale API and ownership](api/tailscale.md) | [Remote setup](startup.md#remote-access) for configuration; [shared access boundaries](api/README.md#access-and-identity-boundaries) |
| Cloudflare desktop access, account setup or service publishing | [Cloudflare API and ownership](api/cloudflare.md) | [Cloudflare setup](startup.md#cloudflare-access) for deployment; [project previews](api/project-previews.md) for the separate local workflow; [shared access boundaries](api/README.md#access-and-identity-boundaries) |
| Shared editors, rendering, filesystem primitives, diagnostics or downloads | [Shared infrastructure](architecture.md#shared-infrastructure) | The consuming feature reference for target, state and lifetime ownership |
| Build tooling, tests or acceptance | [Development](development.md) | The affected feature reference |
| Release preparation, distribution or third-party notices | [Releasing](releasing.md) | [Third-party notices](../THIRD_PARTY_NOTICES.md) for distribution boundaries and upstream sources; [Development](development.md) and [image recipes](../images/guest/README.md) for required acceptance |
| Product decisions or capability claims | [Product direction](product.md) and [project status](../README.md#project-status) | The affected usage or implementation reference |
| User actions and session behavior | [Using AXV](usage.md) | [Startup](startup.md) for installation and CLI workflows |

Contribution, support, conduct and vulnerability-reporting paths are linked from the [project README](../README.md#contributing-and-support).

## Cross-cutting context

Apply these reads only to affected boundaries:

- UI and interaction changes: [Design](design.md), including [acceptance checks](design.md#acceptance-checks).
- Service startup/lifecycle, deployment, environment or configuration changes: [Startup](startup.md).
- HTTP, WebSocket or guest protocol changes: shared rules in the [API index](api/README.md) and the affected contract.
- Product boundary changes: [Product direction](product.md). Existing implementation work does not require rereading unrelated goals.

## Content ownership

Each fact has one owner. Another page may state the consequence its reader needs, then link to the owner for the mechanism, complete contract or procedure.

| Owner | Keep here |
| --- | --- |
| [Project README](../README.md) | Current capability and native-validation status, pinned toolchain summary and quickstart |
| [AGENTS.md](../AGENTS.md) | Repository-wide working rules and brief high-risk guardrails |
| [Contributing](../CONTRIBUTING.md) | AXV contribution workflow and links to organization expectations |
| [Support](../SUPPORT.md) | Public support channels, report context and diagnostic redaction |
| [Code of Conduct](../CODE_OF_CONDUCT.md) | Organization conduct rules and AXV's incident-reporting channels |
| [Security policy](../SECURITY.md) | Security support scope, private vulnerability reporting and disclosure process |
| [This map](README.md) | Reading paths, content ownership and documentation maintenance |
| [Product direction](product.md) | Identity, rationale, settled direction and open product decisions |
| [Architecture overview](architecture.md) | Component dependencies, identity boundaries and cross-service lifetimes |
| [Desktop architecture](desktop.md) | Application registration, mounted windows, menu ownership, layout persistence and Host Terminal ownership |
| [Document storage](storage.md) | Saves, draft recovery, snapshot/exchange mechanisms, database baseline and transactional initialization |
| [Host Codex architecture](codex.md) | Provider integration, process/run ownership, reconciliation and delivery persistence |
| [VM architecture](vm-platform.md) | Host/guest ownership, durable compute state, preparation and provider mechanisms |
| [API reference](api/README.md) | Routes, fields, protocol limits, errors, access checks and caller retry/lifecycle obligations |
| [Startup](startup.md) | Installation, uninstallation, CLI workflows, configuration precedence, paths, environment and service operation |
| [Usage](usage.md) | Visible user actions, confirmations and their effects |
| [Development](development.md) | Build/verification commands, prerequisites, test isolation and diagnostics |
| [Releasing](releasing.md) | Version selection, candidate review, promotion and artifact publication |
| [Third-party notices](../THIRD_PARTY_NOTICES.md) | Distribution boundaries, upstream sources and retained license notices |
| [Design](design.md) | Visual language, interaction, focus and accessibility |
| [Image recipes](../images/guest/README.md) | Image-building inputs, prerequisites and commands |

Manifests and lockfiles declare toolchain versions; the README summarizes them. Code and tests establish current behavior. Rules and product decisions constrain intended changes: if implementation disagrees, identify and resolve the discrepancy explicitly rather than silently rewriting a rule to match it.

## Maintain the map

For each change:

1. Find the owner above and verify changed claims against handlers, models, configuration or tests. Update that owner first; remove or shorten duplicate explanations in callers and link to the owner. Keep useful cross-audience consequences.
2. Update affected dependents. Capability changes also need the README, Welcome, About and page metadata aligned. Contract changes need the API page; visible behavior needs usage/design; configuration and CLI changes need startup. Do not touch unrelated pages merely to repeat the update.
3. Review relative links and anchors, then run the [documentation checks](development.md#verification). Preserve useful anchors or update all inbound links when moving a section.

Keep the structure small:

- Extend an existing owner before adding a page. Split for a distinct responsibility or reading task, then update this map and any affected API navigation. Do not add parallel rule files or summaries that track the same facts.
- Keep `AGENTS.md` short and applicable across the repository. Keep context selection here and dependency direction in the architecture overview; put feature mechanisms in their owner. Add scoped rules only for a real directory-specific requirement.
- Link to a few stable implementation entry points and nearby tests. Avoid exhaustive file inventories, line-number links and copied source declarations. Keep exact values where callers/operators need them, with a source link for review.
- Use descriptive headings, relative links, tables for comparable values and Mermaid for relationships. Examples must match current interfaces.
- Keep plans, audit findings, historical counts and validation diaries in task/PR history. References describe current behavior; product owns intent, and the README owns status. Seeded samples are user documents, not release notes.
